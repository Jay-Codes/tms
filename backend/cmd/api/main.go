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
	"syscall"

	"tms/backend/internal/cache"
	"tms/backend/internal/config"
	"tms/backend/internal/db"
	"tms/backend/internal/httpserver"
	"tms/backend/internal/notify"
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

	os.Exit(serve(cfg, logger))
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

	minioClient, err := storage.Open(cfg.MinioEndpoint, cfg.MinioAccessKey, cfg.MinioSecretKey, cfg.MinioUseSSL)
	if err != nil {
		logger.Warn("minio unavailable; running without object storage", "error", err)
	} else {
		if pingErr := minioClient.Ping(ctx); pingErr != nil {
			logger.Warn("minio ping failed; running degraded", "error", pingErr)
		}
		deps.Minio = minioClient
		deps.Storage = minioClient
	}

	srv := httpserver.New(cfg, deps, logger)
	if err := srv.ListenAndServe(ctx); err != nil {
		logger.Error("server stopped", "error", err)
		return 1
	}
	return 0
}

func providerName(p notify.SMSProvider) string {
	if _, ok := p.(*notify.BeemProvider); ok {
		return "beem"
	}
	return "log"
}
