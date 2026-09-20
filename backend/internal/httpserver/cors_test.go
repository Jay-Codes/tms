package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSNoopWhenUnconfigured(t *testing.T) {
	h := CORS(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
	req.Header.Set("Origin", "https://tms.kuzo.co.tz")
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unexpected ACAO %q with empty allowlist", got)
	}
}

func TestCORSAllowsListedOriginWithCredentials(t *testing.T) {
	h := CORS([]string{"https://tms.kuzo.co.tz", "https://lms.kuzo.co.tz/"})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	for _, origin := range []string{"https://tms.kuzo.co.tz", "https://lms.kuzo.co.tz"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		req.Header.Set("Origin", origin)
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Fatalf("origin %s: ACAO = %q", origin, got)
		}
		if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
			t.Fatalf("origin %s: credentials not allowed", origin)
		}
		if rec.Header().Get("Vary") == "" {
			t.Fatalf("origin %s: Vary missing", origin)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Origin", "https://evil.example")
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unlisted origin got ACAO %q", got)
	}
	if rec.Code != 200 {
		t.Fatalf("unlisted origin must still reach the handler, got %d", rec.Code)
	}
}

func TestCORSPreflightShortCircuits(t *testing.T) {
	called := false
	h := CORS([]string{"https://tms-admin.kuzo.co.tz"})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/admin/orgs", nil)
	req.Header.Set("Origin", "https://tms-admin.kuzo.co.tz")
	req.Header.Set("Access-Control-Request-Method", "PATCH")
	req.Header.Set("Access-Control-Request-Headers", "content-type")
	h.ServeHTTP(rec, req)
	if called {
		t.Fatal("preflight reached the router")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "content-type" {
		t.Fatalf("ACAH = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatal("ACAM missing")
	}
}
