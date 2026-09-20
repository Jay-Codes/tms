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
	"tms/backend/internal/storage"
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

	// Storage issues the presigned URLs and holds the QR PNGs. A nil client
	// makes the QR routes answer 503 rather than panicking.
	Storage *storage.Client
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
	// templates is the Phase 14 platform SMS catalogue: the DB layer between
	// an org's own wording and the built-in Go defaults, cached in Redis.
	templates *notify.PlatformStore

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
		Q:        s.q,
		Redis:    redisClient,
		TTL:      cfg.SessionTTL(),
		Secure:   cfg.CookieSecure(),
		SameSite: cfg.CookieSameSite(),
		Logger:   logger,
	}
	s.store = &auth.Store{Redis: redisClient}
	// One catalogue per process, installed for notify.Render to resolve
	// against. Without Postgres it resolves nothing and Render falls through
	// to the code defaults, which is the correct degraded behaviour: a rent
	// reminder should not be blocked by an unreadable wording table.
	s.templates = &notify.PlatformStore{Q: s.q, Redis: redisClient, Logger: logger}
	notify.UsePlatformStore(s.templates)
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
	// Cross-origin apps (CORS_ALLOWED_ORIGINS). Before routing so preflights
	// never reach the 405 handler.
	r.Use(CORS(s.cfg.CORSAllowedOrigins))

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
			// Phase 13: an org user's own language. It names no member id —
			// it is always the caller's own row.
			r.Patch("/org/members/me", s.handlePatchMemberLocale)
			r.Get("/audit-log", s.handleListAuditLog)
			r.Get("/audit-log/{id}", s.handleGetAuditEntry)

			// --- Phase 2: payment periods ---
			r.Get("/org/payment-periods", s.handleListPaymentPeriods)
			r.Post("/org/payment-periods", s.handleCreatePaymentPeriod)
			r.Post("/org/payment-periods/restore-recommended", s.handleRestoreRecommendedPeriods)
			r.Patch("/org/payment-periods/{id}", s.handlePatchPaymentPeriod)
			r.Delete("/org/payment-periods/{id}", s.handleDeletePaymentPeriod)
			// Part 2: the "Recommended" badge is exclusive and moves through
			// its own endpoint (PLAN2 #8) — PATCH cannot express a write to
			// the period that loses it.
			r.Post("/org/payment-periods/{id}/recommend", s.handleRecommendPaymentPeriod)

			// --- Phase 2: properties ---
			r.Get("/properties", s.handleListProperties)
			r.Post("/properties", s.handleCreateProperty)
			r.Get("/properties/{id}", s.handleGetProperty)
			r.Patch("/properties/{id}", s.handlePatchProperty)
			r.Delete("/properties/{id}", s.handleDeleteProperty)
			r.Get("/properties/{id}/units", s.handleListPropertyUnits)
			r.Post("/properties/{id}/units", s.handleCreateUnit)
			r.Post("/properties/{id}/units/bulk", s.handleBulkCreateUnits)
			r.Get("/properties/{id}/qr-sheet", s.handleQRSheet)

			// --- Phase 2: units, prices ---
			r.Get("/units", s.handleListUnits)
			r.Post("/units/bulk-price", s.handleBulkPrice)
			r.Get("/units/{id}", s.handleGetUnit)
			r.Patch("/units/{id}", s.handlePatchUnit)
			r.Delete("/units/{id}", s.handleDeleteUnit)
			r.Post("/units/{id}/qr", s.handleUnitQR)
			r.Get("/units/{id}/prices", s.handleListPrices)
			r.Post("/units/{id}/prices", s.handleCreatePrice)

			// --- Phase 3: link-request inbox, renter directory ---
			r.Get("/link-requests", s.handleListLinkRequests)
			r.Get("/link-requests/{id}", s.handleGetLinkRequest)
			r.Post("/link-requests/{id}/approve", s.handleApproveLinkRequest)
			r.Post("/link-requests/{id}/reject", s.handleRejectLinkRequest)
			r.Get("/renters", s.handleListRenters)
			r.Get("/renters/{user_id}", s.handleGetRenter)
			r.Get("/renters/{user_id}/kyc-doc", s.handleRenterKYCDoc)

			// --- Phase 4: contract templates ---
			r.Get("/contract-templates", s.handleListTemplates)
			r.Post("/contract-templates", s.handleCreateTemplate)
			r.Get("/contract-templates/{id}", s.handleGetTemplate)
			r.Patch("/contract-templates/{id}", s.handlePatchTemplate)
			r.Delete("/contract-templates/{id}", s.handleDeleteTemplate)
			r.Post("/contract-templates/{id}/preview", s.handlePreviewTemplate)

			// --- Phase 4: contracts ---
			r.Get("/contracts", s.handleListContracts)
			r.Post("/contracts", s.handleCreateContract)
			r.Post("/contracts/{id}/activate", s.handleActivateContract)
			r.Post("/contracts/{id}/terminate", s.handleTerminateContract)

			// --- Phase 5: offline payments, schedules, bank account ---
			r.Get("/schedules", s.handleListSchedules)
			r.Post("/payments", s.handleRecordPayment)
			r.Get("/payments", s.handleListPayments)
			r.Get("/payments/{id}", s.handleGetPayment)
			r.Post("/payments/{id}/reverse", s.handleReversePayment)
			r.Get("/org/bank-account", s.handleGetBankAccount)
			r.Put("/org/bank-account", s.handlePutBankAccount)

			// --- Phase 6: notification settings, bulk SMS, delivery log ---
			r.Get("/org/notification-settings", s.handleGetNotificationSettings)
			r.Put("/org/notification-settings", s.handlePutNotificationSettings)
			r.Post("/notifications/custom", s.handleCustomSMS)
			// Phase 13: the compose screen's per-language recipient counts,
			// taken with the same filters as the send.
			r.Get("/notifications/custom/recipients-preview", s.handleCustomRecipientsPreview)
			// --- Phase 14: the org's own prepaid SMS balance (read-only) ---
			r.Get("/org/sms-credits", s.handleOrgSMSCredits)
			r.Get("/notifications/log", s.handleListNotificationLog)
			r.Post("/notifications/log/{id}/retry", s.handleRetryNotification)

			// --- Phase 7: reports ---
			r.Get("/reports/summary", s.handleReportSummary)
			r.Get("/reports/payment-status", s.handleReportPaymentStatus)
			r.Get("/reports/collections", s.handleReportCollections)

			// --- Phase 11: the cadence-driven series ---
			//
			// Same audience and roles as the Phase 7 reports above: whoever may
			// read the summary may read the chart behind it.
			r.Get("/reports/revenue", s.handleReportRevenue)
			r.Get("/reports/occupancy", s.handleReportOccupancy)

			// --- Phase 16 §16.3: what falls due next ---
			r.Get("/reports/upcoming", s.handleReportUpcoming)

			// --- Phase 10: the expense ledger ---
			//
			// Owner and manager, the org's two roles: whoever may record a
			// payment may record an expense (PLAN2 Phase 10). The roles are
			// named rather than left implicit so a later, narrower role does
			// not inherit the ledger by default.
			r.Group(func(r chi.Router) {
				r.Use(s.sessions.RequireOrg(auth.RoleOwner, auth.RoleManager))
				r.Get("/org/expense-categories", s.handleListExpenseCategories)
				r.Post("/org/expense-categories", s.handleCreateExpenseCategory)
				r.Patch("/org/expense-categories/{id}", s.handlePatchExpenseCategory)
				r.Delete("/org/expense-categories/{id}", s.handleDeleteExpenseCategory)

				r.Get("/expenses", s.handleListExpenses)
				r.Post("/expenses", s.handleCreateExpense)
				r.Get("/expenses/summary", s.handleExpenseSummary)
				r.Get("/expenses/{id}", s.handleGetExpense)
				r.Patch("/expenses/{id}", s.handlePatchExpense)
				r.Post("/expenses/{id}/void", s.handleVoidExpense)
				r.Post("/expenses/{id}/receipt", s.handleExpenseReceiptUpload)
				r.Post("/expenses/{id}/receipt/complete", s.handleExpenseReceiptComplete)
				r.Get("/expenses/{id}/receipt", s.handleExpenseReceiptView)
				r.Delete("/expenses/{id}/receipt", s.handleExpenseReceiptDelete)

				// --- Phase 16 §16.1: the proof-of-payment review queue ---
				//
				// Same two roles as the ledger, for the same reason: accepting
				// a proof records a payment, so whoever may record one may
				// rule on a claim.
				r.Get("/proofs", s.handleListProofs)
				r.Get("/proofs/summary", s.handleProofSummary)
				r.Get("/proofs/{id}", s.handleGetProof)
				r.Post("/proofs/{id}/accept", s.handleAcceptProof)
				r.Post("/proofs/{id}/reject", s.handleRejectProof)

				// --- Phase 16 §16.2: CSV import of previous records ---
				//
				// Owner and manager again: an import creates units, renters and
				// payments, so it needs exactly the rights those endpoints do
				// and no more. The template route sits above `/imports/{id}` —
				// chi matches the static segment first.
				r.Get("/imports/templates/{kind}", s.handleImportTemplate)
				r.Post("/imports/preview", s.handleImportPreview)
				r.Get("/imports", s.handleListImports)
				r.Get("/imports/{id}", s.handleGetImport)
				r.Post("/imports/{id}/commit", s.handleCommitImport)
				r.Post("/imports/{id}/undo", s.handleUndoImport)
			})

			// --- Phase 4: branding ---
			r.Get("/org/branding", s.handleGetBranding)
			r.Put("/org/branding", s.handlePutBranding)
			r.Post("/org/branding/logo", s.handleBrandingUpload)
			r.Post("/org/branding/logo/complete", s.handleBrandingUploadComplete)
			r.Delete("/org/branding/logo", s.handleBrandingDelete)
			r.Post("/org/branding/letterhead", s.handleBrandingUpload)
			r.Post("/org/branding/letterhead/complete", s.handleBrandingUploadComplete)
			r.Delete("/org/branding/letterhead", s.handleBrandingDelete)
		})

		// --- Phase 3: renter scope (tms_r) ---
		r.Group(func(r chi.Router) {
			r.Use(s.sessions.RequireRenter())
			// Phase 13: the renter's own language (renter Profile).
			r.Patch("/me", s.handlePatchMyLocale)
			r.Get("/me/profile", s.handleGetMyProfile)
			r.Put("/me/profile", s.handlePutMyProfile)
			r.Post("/me/profile/kyc-upload", s.handleKYCUpload)
			r.Post("/me/profile/kyc-upload/complete", s.handleKYCUploadComplete)
			r.Get("/me/profile/kyc-doc", s.handleMyKYCDoc)
			r.Get("/me/link-requests", s.handleListMyLinkRequests)
			r.Delete("/me/link-requests/{id}", s.handleCancelMyLinkRequest)
			r.Post("/units/{unit_code}/link", s.handleCreateLinkRequest)

			// --- Phase 4: the renter's own contracts and money ---
			r.Get("/me/contracts", s.handleListMyContracts)
			r.Get("/me/schedules", s.handleMySchedules)
			r.Post("/contracts/{id}/sign/otp", s.handleContractSignOTP)
			r.Post("/contracts/{id}/signature-upload", s.handleSignatureUpload)
			r.Post("/contracts/{id}/sign", s.handleSignContract)

			// --- Phase 5: the renter's own payment history ---
			r.Get("/me/payments", s.handleListMyPayments)

			// --- Phase 16 §16.1: the renter's proofs of payment ---
			r.Post("/me/proofs/upload", s.handleProofUpload)
			r.Post("/me/proofs", s.handleCreateProof)
			r.Get("/me/proofs", s.handleListMyProofs)
			r.Delete("/me/proofs/{id}", s.handleWithdrawProof)
		})

		// --- Phase 4: contract reads, open to either party (tms_o or tms_r) ---
		// One document, two readers (SPEC §5.5); the handlers scope by
		// whichever principal arrives, so the other party's contract is a 404.
		r.Group(func(r chi.Router) {
			r.Use(s.sessions.RequireContractParty())
			r.Get("/contracts/{id}", s.handleGetContract)
			r.Get("/contracts/{id}/document", s.handleContractDocument)
			r.Get("/contracts/{id}/verify", s.handleVerifyContract)
			r.Get("/contracts/{id}/schedules", s.handleContractSchedules)
		})

		// --- Phase 4/7: platform admin (tms_a) ---
		r.Group(func(r chi.Router) {
			r.Use(s.sessions.RequireAdmin())
			r.Post("/admin/jobs/contract-lifecycle", s.handleContractLifecycleJob)
			r.Post("/admin/jobs/overdue", s.handleOverdueJob)
			r.Post("/admin/jobs/notifications", s.handleNotificationsJob)

			// --- Phase 7: org supervision, platform metrics, audit search ---
			r.Get("/admin/jobs", s.handleAdminJobs)
			r.Get("/admin/orgs", s.handleAdminListOrgs)
			r.Get("/admin/orgs/{id}", s.handleAdminGetOrg)
			r.Post("/admin/orgs/{id}/suspend", s.handleAdminSuspendOrg)
			r.Post("/admin/orgs/{id}/activate", s.handleAdminActivateOrg)
			r.Get("/admin/metrics", s.handleAdminMetrics)
			r.Get("/admin/audit-log", s.handleAdminAuditLog)

			// --- Phase 14: prepaid SMS credits per org ---
			r.Get("/admin/orgs/{id}/sms", s.handleAdminOrgSMS)
			r.Patch("/admin/orgs/{id}/sms", s.handleAdminOrgSMSWatermark)
			r.Post("/admin/orgs/{id}/sms/topup", s.handleAdminOrgSMSTopup)
			r.Post("/admin/orgs/{id}/sms/adjust", s.handleAdminOrgSMSAdjust)

			// --- Phase 14: the editable platform SMS catalogue ---
			r.Get("/admin/templates", s.handleAdminListTemplates)
			r.Put("/admin/templates/{kind}", s.handleAdminPutTemplate)
			r.Patch("/admin/templates/{kind}", s.handleAdminPatchTemplate)
			r.Post("/admin/templates/{kind}/preview", s.handleAdminPreviewTemplate)
			r.Get("/admin/templates/{kind}/versions", s.handleAdminTemplateVersions)
			r.Post("/admin/templates/{kind}/revert", s.handleAdminRevertTemplate)
		})

		// --- Phase 2: public (no session; rate limited per IP) ---
		r.Get("/public/orgs/{slug}/branding", s.publicRateLimited(s.handlePublicBranding))
		r.Get("/public/units/{unit_code}", s.publicRateLimited(s.handlePublicUnit))
		// --- Phase 12: the shipped theme presets (public, cacheable) ---
		r.Get("/themes/presets", s.publicRateLimited(s.handleThemePresets))
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
