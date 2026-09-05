// Package httpx holds the transport primitives shared by the router and the
// middleware packages: RFC-7807 problem documents, JSON encode/decode helpers
// and request metadata extraction. It exists so packages below httpserver
// (auth, for example) can emit consistent errors without an import cycle.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
)

// ProblemContentType is the media type mandated by RFC 7807.
const ProblemContentType = "application/problem+json"

// MaxBodyBytes caps decoded request bodies (1 MiB).
const MaxBodyBytes = 1 << 20

// Problem is an RFC 7807 problem detail document. `errors` carries per-field
// validation messages (API.md).
type Problem struct {
	Type     string            `json:"type"`
	Title    string            `json:"title"`
	Status   int               `json:"status"`
	Detail   string            `json:"detail,omitempty"`
	Instance string            `json:"instance,omitempty"`
	Errors   map[string]string `json:"errors,omitempty"`
}

// WriteProblem writes an RFC 7807 error response.
func WriteProblem(w http.ResponseWriter, status int, title, detail string) {
	WriteProblemFields(w, status, title, detail, nil)
}

// WriteProblemFields writes an RFC 7807 error response with field errors.
func WriteProblemFields(w http.ResponseWriter, status int, title, detail string, fields map[string]string) {
	p := Problem{Type: "about:blank", Title: title, Status: status, Detail: detail, Errors: fields}
	w.Header().Set("Content-Type", ProblemContentType)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(p); err != nil {
		slog.Error("write problem response", "error", err)
	}
}

// WriteProblemCode writes an RFC 7807 error carrying a machine-readable code
// in `type`, for the errors API.md names by code (`unit_occupied`,
// `kyc_required`, …). Clients branch on the code; humans read the title.
func WriteProblemCode(w http.ResponseWriter, status int, code, title, detail string) {
	p := Problem{Type: code, Title: title, Status: status, Detail: detail}
	w.Header().Set("Content-Type", ProblemContentType)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(p); err != nil {
		slog.Error("write problem response", "error", err)
	}
}

// WriteJSON writes v as a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write json response", "error", err)
	}
}

// NoContent writes a 204 response.
func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// EncodeJSON writes v to an already-started response: the caller has set the
// status and content type itself.
func EncodeJSON(w http.ResponseWriter, v any) {
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write json response", "error", err)
	}
}

// DecodeJSON reads a JSON request body into dst, rejecting unknown fields and
// oversized bodies. On failure it writes an RFC 7807 problem and returns false.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			WriteProblem(w, http.StatusRequestEntityTooLarge, "request body too large",
				fmt.Sprintf("the request body must not exceed %d bytes", MaxBodyBytes))
			return false
		}
		WriteProblem(w, http.StatusBadRequest, "invalid request body", err.Error())
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		WriteProblem(w, http.StatusBadRequest, "invalid request body", "body must contain a single JSON object")
		return false
	}
	return true
}

// ProxyTrust decides whether the direct peer of a request is allowed to set
// X-Forwarded-For / X-Real-IP on the caller's behalf. Anything outside the
// trusted set has its forwarded headers ignored, so audit IPs and rate-limit
// buckets cannot be spoofed by an arbitrary client.
type ProxyTrust struct {
	nets []*net.IPNet
}

// NewProxyTrust parses a comma-separated CIDR list. An empty list means "trust
// nothing": forwarded headers are then always ignored.
func NewProxyTrust(cidrs string) (ProxyTrust, error) {
	var t ProxyTrust
	for _, raw := range strings.Split(cidrs, ",") {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return ProxyTrust{}, fmt.Errorf("httpx: invalid proxy CIDR %q: %w", s, err)
		}
		t.nets = append(t.nets, n)
	}
	return t, nil
}

// Trusts reports whether ip (the direct peer) may set forwarded headers.
func (t ProxyTrust) Trusts(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range t.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientIP extracts the caller's IP. X-Forwarded-For / X-Real-IP are honoured
// only when the direct peer (RemoteAddr) is inside the trusted proxy set;
// otherwise the peer address itself is returned.
func (t ProxyTrust) ClientIP(r *http.Request) string {
	peer := r.RemoteAddr
	if host, _, err := net.SplitHostPort(peer); err == nil {
		peer = host
	}
	peer = strings.Trim(peer, "[]")

	if !t.Trusts(net.ParseIP(peer)) {
		return peer
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
			return first
		}
	}
	if xrip := strings.TrimSpace(r.Header.Get("X-Real-IP")); xrip != "" {
		return xrip
	}
	return peer
}
