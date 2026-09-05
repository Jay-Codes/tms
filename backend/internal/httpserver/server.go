// Package httpserver wires the chi router, middleware stack, JSON/RFC-7807
// helpers and the Phase 1 auth/org/audit endpoints for the TMS API.
package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"tms/backend/internal/auth"
	"tms/backend/internal/cache"
	"tms/backend/internal/config"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/notify"
	"tms/backend/internal/ratelimit"
)

// APIPrefix is the mount point for every versioned route.
const APIPrefix = "/api/v1"

const (
	readHeaderTimeout = 10 * time.Second
	writeTimeout      = 60 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownTimeout   = 10 * time.Second
)

// Deps are the external dependencies the API serves against.
//
// DB/Redis/Minio are the health-check probes and may be nil. Pool and Cache are
// the concrete handles used by the data-backed routes: when Pool is nil those
// routes answer 503 rather than panicking, keeping /healthz meaningful.
type Deps struct {
	DB    Pinger
	Redis Pinger
	Minio Pinger

	Pool  *db.Pool
	Cache *cache.Client
	SMS   notify.SMSProvider
	Email notify.EmailProvider
}

// Server is the TMS HTTP API.
type Server struct {
	cfg      config.Config
	deps     Deps
	logger   *slog.Logger
	router   chi.Router
	http     *http.Server
	q        *sqlc.Queries
	sessions *auth.Manager
	store    *auth.Store
	limiter  *ratelimit.Limiter

	proxyTrust httpx.ProxyTrust
}

// New builds a Server with the full middleware stack and routes mounted.
func New(cfg config.Config, deps Deps, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{cfg: cfg, deps: deps, logger: logger}

	trust, err := httpx.NewProxyTrust(cfg.TrustedProxyCIDRs)
	if err != nil {
		logger.Error("invalid TRUSTED_PROXY_CIDRS; trusting no proxy", "error", err)
		trust = httpx.ProxyTrust{}
	}
	s.proxyTrust = trust

	if deps.Pool != nil {
		s.q = sqlc.New(deps.Pool)
	}
	var redisClient = redisOf(deps.Cache)
	s.sessions = &auth.Manager{
		Q:      s.q,
		Redis:  redisClient,
		TTL:    cfg.SessionTTL(),
		Secure: cfg.CookieSecure(),
		Logger: logger,
	}
	s.store = &auth.Store{Redis: redisClient}
	s.limiter = ratelimit.New(redisClient, logger)
	// Defaults for tests and the dev loop. In ENV=prod the dev log providers
	// are refused (they would print OTP codes and invite links to the log);
	// cmd/api fails startup on the same condition before reaching here.
	if s.deps.SMS == nil {
		p, err := notify.SMSProviderFor(cfg, logger)
		if err != nil {
			logger.Error("sms provider unavailable", "error", err)
			p = notify.DisabledSMSProvider{}
		}
		s.deps.SMS = p
	}
	if s.deps.Email == nil {
		p, err := notify.EmailProviderFor(cfg, logger)
		if err != nil {
			logger.Error("email provider unavailable", "error", err)
			p = notify.DisabledEmailProvider{}
		}
		s.deps.Email = p
	}

	s.router = s.routes()
	s.http = &http.Server{
		Addr:              cfg.Addr(),
		Handler:           s.router,
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
	return s
}

// Handler exposes the router (used by tests).
func (s *Server) Handler() http.Handler { return s.router }

// Sessions exposes the session manager (used by tests to mint sessions).
func (s *Server) Sessions() *auth.Manager { return s.sessions }

func (s *Server) routes() chi.Router {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	// No middleware.RealIP: it rewrites RemoteAddr from client-supplied
	// headers. RequestContext resolves the client IP against the trusted
	// proxy set instead.
	r.Use(SlogLogger(s.logger))
	r.Use(middleware.Recoverer)
	r.Use(RequestContext(s.proxyTrust))

	r.NotFound(NotFound)
	r.MethodNotAllowed(MethodNotAllowed)

	r.Route(APIPrefix, func(r chi.Router) {
		r.Get("/healthz", s.handleHealthz)

		// --- auth (public / mixed audience) ---
		r.Route("/auth", func(r chi.Router) {
			r.Post("/otp/send", s.handleOTPSend)
			r.Post("/otp/verify", s.handleOTPVerify)
			r.Post("/register/renter", s.handleRegisterRenter)
			r.Post("/login", s.handleLogin)
			r.Post("/logout", s.handleLogout)
			r.Get("/me", s.handleMe)
			r.Post("/verify-email", s.handleVerifyEmail)
			r.Post("/invite/accept", s.handleInviteAccept)

			r.Group(func(r chi.Router) {
				r.Use(s.sessions.RequireOrg())
				r.Post("/verify-email/resend", s.handleVerifyEmailResend)
			})
		})

		// --- org signup (public) ---
		r.Post("/orgs", s.handleCreateOrg)

		// --- org scope (tms_o) ---
		r.Group(func(r chi.Router) {
			r.Use(s.sessions.RequireOrg())
			r.Get("/org", s.handleGetOrg)
			r.Patch("/org", s.handlePatchOrg)
			r.Get("/org/members", s.handleListMembers)
			r.Get("/audit-log", s.handleListAuditLog)
			r.Get("/audit-log/{id}", s.handleGetAuditEntry)
		})
		r.Group(func(r chi.Router) {
			r.Use(s.sessions.RequireOrg(auth.RoleOwner))
			r.Post("/org/members", s.handleCreateMember)
			r.Delete("/org/members/{id}", s.handleDeleteMember)
		})
	})

	return r
}

// ListenAndServe starts the server and blocks until ctx is cancelled, then
// shuts down gracefully.
func (s *Server) ListenAndServe(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("api listening", "addr", s.cfg.Addr(), "env", string(s.cfg.Env))
		if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		s.logger.Info("api shutting down")
		return s.http.Shutdown(shutdownCtx)
	}
}
