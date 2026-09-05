// Command api is the single TMS backend service: REST API, migrations runner
// and (from Phase 6) the notification scheduler.
//
// Usage:
//
//	api                 # serve the HTTP API
//	api migrate up      # apply pending migrations
//	api migrate down    # roll back one migration
//	api -migrate        # alias for `migrate up`
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/redis/go-redis/v9"

	"tms/backend/internal/cache"
	"tms/backend/internal/config"
	"tms/backend/internal/contract"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpserver"
	"tms/backend/internal/notify"
	"tms/backend/internal/payment"
	"tms/backend/internal/platform"
	"tms/backend/internal/storage"
)

func main() {
	migrateFlag := flag.Bool("migrate", false, "apply pending migrations and exit")
	flag.Parse()

	cfg := config.Load()
	logger := newLogger(cfg)
	slog.SetDefault(logger)

	// Subcommand form: `api migrate up|down`.
	if args := flag.Args(); len(args) > 0 && args[0] == "migrate" {
		direction := "up"
		if len(args) > 1 {
			direction = args[1]
		}
		runMigrate(cfg, logger, direction)
		return
	}
	if *migrateFlag {
		runMigrate(cfg, logger, "up")
		return
	}

	// MIGRATE_ON_START=1 applies pending migrations in-process before the
	// server binds. Used by the compose `full` profile, where the API image is
	// the only thing that owns the schema and there is no separate migration
	// step. A failure here is fatal: serving an out-of-date schema is worse
	// than not starting.
	if migrateOnStart() {
		logger.Info("MIGRATE_ON_START set; applying migrations before serving")
		runMigrate(cfg, logger, "up")
	}

	os.Exit(serve(cfg, logger))
}

// migrateOnStart reports whether MIGRATE_ON_START asks for migrations at boot.
// Anything strconv.ParseBool accepts as true (1, t, true, TRUE…) enables it.
func migrateOnStart() bool {
	v, ok := os.LookupEnv("MIGRATE_ON_START")
	if !ok {
		return false
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	return err == nil && b
}

func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if cfg.IsDev() {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}

func runMigrate(cfg config.Config, logger *slog.Logger, direction string) {
	var err error
	switch direction {
	case "up":
		err = db.MigrateUp(cfg.DatabaseURL, logger)
	case "down":
		err = db.MigrateDown(cfg.DatabaseURL, logger)
	default:
		logger.Error("unknown migrate direction", "direction", direction, "want", "up|down")
		os.Exit(2)
	}
	if err != nil {
		logger.Error("migration failed", "error", err)
		os.Exit(1)
	}
}

// serve wires dependencies and runs the HTTP server. Only a failure to bind
// the listener is fatal: unreachable Postgres, Redis or MinIO degrade the
// service (reported by /healthz) rather than blocking the dev loop.
func serve(cfg config.Config, logger *slog.Logger) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	deps := httpserver.Deps{}

	// Notification providers are fatal: in ENV=prod the dev log providers are
	// refused (they would write OTP codes and invite links to the log), so the
	// binary must not start rather than degrade.
	sms, err := notify.SMSProviderFor(cfg, logger)
	if err != nil {
		logger.Error("sms provider unavailable; refusing to start", "env", string(cfg.Env), "error", err)
		return 2
	}
	email, err := notify.EmailProviderFor(cfg, logger)
	if err != nil {
		logger.Error("email provider unavailable; refusing to start", "env", string(cfg.Env), "error", err)
		return 2
	}
	deps.SMS = sms
	deps.Email = email
	logger.Info("sms provider selected", "beem_configured", cfg.BeemAPIKey != "", "provider_type", providerName(deps.SMS))

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("postgres unavailable at startup; serving degraded", "error", err)
	} else {
		defer pool.Close()
		if pingErr := pool.Ping(ctx); pingErr != nil {
			logger.Error("postgres ping failed at startup; serving degraded", "error", pingErr)
		}
		deps.DB = pool
		deps.Pool = pool
		if seedErr := platform.SeedAdmin(ctx, pool, cfg, logger); seedErr != nil {
			logger.Error("platform admin seeding failed", "error", seedErr)
		}
	}

	redisClient, err := cache.Open(cfg.RedisURL)
	if err != nil {
		logger.Warn("redis unavailable; running without cache", "error", err)
	} else {
		defer func() { _ = redisClient.Close() }()
		if pingErr := redisClient.Ping(ctx); pingErr != nil {
			logger.Warn("redis ping failed; running degraded", "error", pingErr)
		}
		deps.Redis = redisClient
		deps.Cache = redisClient
	}

	minioClient, err := storage.Open(cfg.MinioEndpoint, cfg.MinioAccessKey, cfg.MinioSecretKey, cfg.MinioUseSSL, cfg.MinioPublicURL)
	if err != nil {
		logger.Warn("minio unavailable; running without object storage", "error", err)
	} else {
		if pingErr := minioClient.Ping(ctx); pingErr != nil {
			logger.Warn("minio ping failed; running degraded", "error", pingErr)
		}
		deps.Minio = minioClient
		deps.Storage = minioClient
	}

	// The notification worker drains the Redis SMS queue and records each
	// send in notification_log. It needs Postgres, Redis and a provider; with
	// any of them missing it declines to start and messages simply stay
	// `queued` until a healthy run picks them up (SPEC §2.2).
	if deps.Pool != nil && redisClient != nil {
		go notify.RunWorker(ctx, notify.Worker{
			Q:       sqlc.New(deps.Pool),
			Redis:   redisClient.Client,
			SMS:     deps.SMS,
			Logger:  logger,
			Workers: cfg.NotifyWorkers,
		})
	} else {
		logger.Warn("notification worker not started: postgres or redis unavailable")
	}

	// The notification scheduler derives the Flow 8 timeline from Postgres
	// every five minutes — reminders before and on the due date, the daily
	// overdue chase, and the nudge for a contract still unsigned — and queues
	// each send under a dedupe key so a repeat tick costs nothing (SPEC §2.2).
	go notify.RunScheduler(ctx, deps.Pool, redisOf(redisClient), logger, notify.Options{
		BaseURL:  cfg.PublicBaseURL,
		Settings: httpserver.SchedulerSettings,
	})

	// The contract lifecycle sweep flags contracts approaching their end date
	// and closes the ones past it. It runs once at startup and hourly after
	// that; it needs only Postgres, and its statements are idempotent
	// (internal/contract.RunLifecycle).
	go contract.RunLifecycleTicker(ctx, deps.Pool, logger)

	// The overdue sweep flips unsettled schedules whose due date (plus the
	// org's grace period) has passed. Same shape as the lifecycle job: once at
	// startup, hourly after that, Postgres only, idempotent
	// (internal/payment.FlipOverdue). The org-scoped reads run it on demand
	// too, so a landlord never reads a stale `pending`.
	go payment.RunOverdueTicker(ctx, deps.Pool, logger)

	srv := httpserver.New(cfg, deps, logger)
	if err := srv.ListenAndServe(ctx); err != nil {
		logger.Error("server stopped", "error", err)
		return 1
	}
	return 0
}

// redisOf unwraps the cache client, tolerating a nil (Redis-less) run.
func redisOf(c *cache.Client) *redis.Client {
	if c == nil {
		return nil
	}
	return c.Client
}

func providerName(p notify.SMSProvider) string {
	if _, ok := p.(*notify.BeemProvider); ok {
		return "beem"
	}
	return "log"
}
