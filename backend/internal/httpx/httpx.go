// Package httpx holds the transport primitives shared by the router and the
// middleware packages: RFC-7807 problem documents, JSON encode/decode helpers
// and request metadata extraction. It exists so packages below httpserver
// (auth, for example) can emit consistent errors without an import cycle.
package httpx

import (
	"encoding/json"
	"errors"
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

// DecodeJSON reads a JSON request body into dst, rejecting unknown fields and
// oversized bodies. On failure it writes an RFC 7807 problem and returns false.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		WriteProblem(w, http.StatusBadRequest, "invalid request body", err.Error())
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		WriteProblem(w, http.StatusBadRequest, "invalid request body", "body must contain a single JSON object")
		return false
	}
	return true
}

// ClientIP extracts the caller's IP, honouring X-Forwarded-For (the dev proxy
// and ngrok both set it) and falling back to RemoteAddr.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
			return first
		}
	}
	if xrip := strings.TrimSpace(r.Header.Get("X-Real-IP")); xrip != "" {
		return xrip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
