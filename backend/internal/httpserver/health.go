package httpserver

import (
	"context"
	"net/http"
	"time"
)

// Pinger is any dependency that can report reachability.
type Pinger interface {
	Ping(ctx context.Context) error
}

const (
	statusOK   = "ok"
	statusDown = "down"
)

// pingTimeout bounds each dependency check in the health handler.
const pingTimeout = 2 * time.Second

// HealthResponse is the body of GET /api/v1/healthz.
type HealthResponse struct {
	Status string `json:"status"`
	DB     string `json:"db"`
	Redis  string `json:"redis"`
	Minio  string `json:"minio"`
}

// pingState returns "ok" or "down" for a possibly-nil dependency.
func pingState(ctx context.Context, p Pinger) string {
	if p == nil {
		return statusDown
	}
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := p.Ping(ctx); err != nil {
		return statusDown
	}
	return statusOK
}

// handleHealthz reports dependency reachability. Postgres is the only hard
// dependency: Redis and MinIO being down is a degraded but serviceable state,
// so only a down database produces 503.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	resp := HealthResponse{
		DB:    pingState(ctx, s.deps.DB),
		Redis: pingState(ctx, s.deps.Redis),
		Minio: pingState(ctx, s.deps.Minio),
	}

	code := http.StatusOK
	resp.Status = statusOK
	if resp.DB != statusOK {
		code = http.StatusServiceUnavailable
		resp.Status = statusDown
	}
	WriteJSON(w, code, resp)
}
