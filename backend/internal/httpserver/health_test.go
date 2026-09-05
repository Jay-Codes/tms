package httpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"tms/backend/internal/config"
	"tms/backend/internal/httpserver"
)

type stubPinger struct{ err error }

func (s stubPinger) Ping(context.Context) error { return s.err }

func doHealthz(t *testing.T, deps httpserver.Deps) (int, httpserver.HealthResponse) {
	t.Helper()
	srv := httpserver.New(config.Config{Port: "0"}, deps, discardLogger())

	req := httptest.NewRequest(http.MethodGet, httpserver.APIPrefix+"/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var body httpserver.HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode healthz body: %v", err)
	}
	return rec.Code, body
}

func TestHealthzNilDepsReturns503(t *testing.T) {
	code, body := doHealthz(t, httpserver.Deps{})

	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", code, http.StatusServiceUnavailable)
	}
	want := httpserver.HealthResponse{Status: "down", DB: "down", Redis: "down", Minio: "down"}
	if body != want {
		t.Fatalf("body = %+v, want %+v", body, want)
	}
}

func TestHealthzAllUpReturns200(t *testing.T) {
	ok := stubPinger{}
	code, body := doHealthz(t, httpserver.Deps{DB: ok, Redis: ok, Minio: ok})

	if code != http.StatusOK {
		t.Fatalf("status = %d, want %d", code, http.StatusOK)
	}
	want := httpserver.HealthResponse{Status: "ok", DB: "ok", Redis: "ok", Minio: "ok"}
	if body != want {
		t.Fatalf("body = %+v, want %+v", body, want)
	}
}

// Redis and MinIO are non-critical: their loss is degraded, not unavailable.
func TestHealthzDBUpCacheDownReturns200(t *testing.T) {
	down := stubPinger{err: errors.New("boom")}
	code, body := doHealthz(t, httpserver.Deps{DB: stubPinger{}, Redis: down, Minio: down})

	if code != http.StatusOK {
		t.Fatalf("status = %d, want %d", code, http.StatusOK)
	}
	if body.Status != "ok" || body.DB != "ok" || body.Redis != "down" || body.Minio != "down" {
		t.Fatalf("body = %+v", body)
	}
}

func TestUnknownRouteReturnsProblemJSON(t *testing.T) {
	srv := httpserver.New(config.Config{Port: "0"}, httpserver.Deps{}, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/nope", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != httpserver.ProblemContentType {
		t.Fatalf("content-type = %q, want %q", ct, httpserver.ProblemContentType)
	}
	var p httpserver.Problem
	if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if p.Status != http.StatusNotFound || p.Title == "" {
		t.Fatalf("problem = %+v", p)
	}
}
