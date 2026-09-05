package httpserver

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"

	"tms/backend/internal/audit"
	"tms/backend/internal/cache"
	"tms/backend/internal/httpx"
)

// SlogLogger logs one structured line per request at INFO (5xx at ERROR).
func SlogLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			defer func() {
				attrs := []any{
					"method", r.Method,
					"path", r.URL.Path,
					"status", ww.Status(),
					"bytes", ww.BytesWritten(),
					"duration_ms", time.Since(start).Milliseconds(),
					"remote_addr", r.RemoteAddr,
					"request_id", middleware.GetReqID(r.Context()),
				}
				if ww.Status() >= http.StatusInternalServerError {
					logger.Error("http request", attrs...)
					return
				}
				logger.Info("http request", attrs...)
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

// RequestContext stores the caller's IP and user agent in the request context
// so audit rows written deeper in the stack carry them (SPEC §8). Forwarded-for
// headers are honoured only for peers inside trust (chi's RealIP middleware is
// deliberately not used: it rewrites RemoteAddr from unauthenticated headers).
func RequestContext(trust httpx.ProxyTrust) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := audit.WithRequestInfo(r.Context(), trust.ClientIP(r), r.UserAgent())
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// redisOf unwraps the cache client, tolerating a nil (Redis-down) client.
func redisOf(c *cache.Client) *redis.Client {
	if c == nil {
		return nil
	}
	return c.Client
}
