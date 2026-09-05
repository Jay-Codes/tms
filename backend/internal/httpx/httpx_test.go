package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tms/backend/internal/config"
	"tms/backend/internal/httpx"
)

func defaultTrust(t *testing.T) httpx.ProxyTrust {
	t.Helper()
	trust, err := httpx.NewProxyTrust(config.DefaultTrustedProxyCIDRs)
	if err != nil {
		t.Fatalf("NewProxyTrust() error = %v", err)
	}
	return trust
}

// TestClientIPIgnoresSpoofedHeaders is the M3 guard: forwarded headers from an
// untrusted peer must not influence the resolved client IP (audit rows and
// rate-limit buckets are keyed on it).
func TestClientIPIgnoresSpoofedHeaders(t *testing.T) {
	trust := defaultTrust(t)

	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		xrip       string
		want       string
	}{
		{"untrusted peer spoofs XFF", "203.0.113.9:51234", "1.2.3.4", "", "203.0.113.9"},
		{"untrusted peer spoofs X-Real-IP", "203.0.113.9:51234", "", "1.2.3.4", "203.0.113.9"},
		{"untrusted peer spoofs both", "198.51.100.7:1", "1.2.3.4, 5.6.7.8", "9.9.9.9", "198.51.100.7"},
		{"untrusted peer, no headers", "203.0.113.9:51234", "", "", "203.0.113.9"},
		{"loopback peer XFF honoured", "127.0.0.1:44444", "1.2.3.4, 5.6.7.8", "", "1.2.3.4"},
		{"loopback peer X-Real-IP honoured", "127.0.0.1:44444", "", "1.2.3.4", "1.2.3.4"},
		{"loopback peer, no headers", "127.0.0.1:44444", "", "", "127.0.0.1"},
		{"ipv6 loopback honoured", "[::1]:44444", "1.2.3.4", "", "1.2.3.4"},
		{"ipv6 untrusted peer", "[2001:db8::1]:44444", "1.2.3.4", "", "2001:db8::1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.xrip != "" {
				r.Header.Set("X-Real-IP", tc.xrip)
			}
			if got := trust.ClientIP(r); got != tc.want {
				t.Errorf("ClientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProxyTrustCustomCIDRAndErrors(t *testing.T) {
	trust, err := httpx.NewProxyTrust("10.0.0.0/8, 127.0.0.0/8")
	if err != nil {
		t.Fatalf("NewProxyTrust() error = %v", err)
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.4.5.6:9999"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := trust.ClientIP(r); got != "1.2.3.4" {
		t.Errorf("ClientIP() = %q, want 1.2.3.4 (peer inside configured CIDR)", got)
	}

	// The empty trust set trusts nobody, loopback included.
	empty, err := httpx.NewProxyTrust("")
	if err != nil {
		t.Fatalf("NewProxyTrust(\"\") error = %v", err)
	}
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.RemoteAddr = "127.0.0.1:1234"
	r2.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := empty.ClientIP(r2); got != "127.0.0.1" {
		t.Errorf("ClientIP() with empty trust = %q, want 127.0.0.1", got)
	}

	if _, err := httpx.NewProxyTrust("not-a-cidr"); err == nil {
		t.Error("NewProxyTrust(\"not-a-cidr\") error = nil, want error")
	}
}

// TestDecodeJSONBodyTooLarge is the L6 guard: an oversized body is a 413, not
// a generic 400.
func TestDecodeJSONBodyTooLarge(t *testing.T) {
	body := `{"a":"` + strings.Repeat("x", httpx.MaxBodyBytes+1024) + `"}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rec := httptest.NewRecorder()

	var dst struct {
		A string `json:"a"`
	}
	if httpx.DecodeJSON(rec, r, &dst) {
		t.Fatal("DecodeJSON() = true for an oversized body")
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	if ct := rec.Header().Get("Content-Type"); ct != httpx.ProblemContentType {
		t.Errorf("Content-Type = %q, want %q", ct, httpx.ProblemContentType)
	}
}

func TestDecodeJSONMalformedIsBadRequest(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"a":`))
	rec := httptest.NewRecorder()
	var dst struct {
		A string `json:"a"`
	}
	if httpx.DecodeJSON(rec, r, &dst) {
		t.Fatal("DecodeJSON() = true for malformed body")
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
