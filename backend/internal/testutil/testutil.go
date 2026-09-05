// Package testutil provides the fixtures handler tests run against: a real
// Postgres (migrated once, truncated between tests) and an in-process Redis.
//
// The database is opt-in. Set TEST_DATABASE_URL, or rely on the default
// (postgres://tms:tms_dev@localhost:5433/tms_test); when it is unreachable the
// helpers skip the test with a printed note instead of failing the suite, so
// `make test` stays green on a machine without the compose stack running.
package testutil

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"tms/backend/internal/cache"
	"tms/backend/internal/db"
)

// DefaultTestDatabaseURL is the dev-compose test database (host port 5433).
const DefaultTestDatabaseURL = "postgres://tms:tms_dev@localhost:5433/tms_test?sslmode=disable"

// tablesToTruncate is every table carrying test state. orgs cascades to the
// org-scoped tables, but listing them keeps truncation explicit and fast.
var tablesToTruncate = []string{
	"audit_log", "sessions", "notification_log", "payments", "payment_schedules",
	"unit_link_requests", "contract_signatures", "contracts", "contract_templates",
	"price_plans", "units", "properties", "renter_profiles", "org_members",
	"org_branding", "payment_periods", "users", "orgs",
}

var (
	migrateOnce sync.Once
	migrateErr  error
)

// DatabaseURL is the DSN handler tests run against.
func DatabaseURL() string {
	if v := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")); v != "" {
		return v
	}
	return DefaultTestDatabaseURL
}

// Pool opens the test database, applying migrations on first use. If the
// database cannot be reached the test is skipped with an explanatory note
// (run `make test-db` to create it).
func Pool(t *testing.T) *db.Pool {
	t.Helper()
	dsn := DatabaseURL()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := db.Open(ctx, dsn)
	if err == nil {
		err = pool.Ping(ctx)
	}
	if err != nil {
		if pool != nil {
			pool.Close()
		}
		t.Skipf("SKIP: test database unreachable at %s (%v)\n"+
			"      run `make up && make test-db` to create it, or set TEST_DATABASE_URL", dsn, err)
	}

	migrateOnce.Do(func() {
		migrateErr = db.MigrateUp(dsn, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	})
	if migrateErr != nil {
		pool.Close()
		t.Fatalf("migrate test database: %v", migrateErr)
	}

	Truncate(t, pool)
	t.Cleanup(pool.Close)
	return pool
}

// Truncate empties every application table, restarting identities.
func Truncate(t *testing.T, pool *db.Pool) {
	t.Helper()
	// audit_log is append-only via triggers; TRUNCATE bypasses row triggers, so
	// the test reset works without weakening the production guarantee.
	stmt := "TRUNCATE " + strings.Join(tablesToTruncate, ", ") + " RESTART IDENTITY CASCADE"
	if _, err := pool.Exec(context.Background(), stmt); err != nil {
		t.Fatalf("truncate test tables: %v", err)
	}
}

// Redis starts an in-process Redis (miniredis) and returns a client wired to
// it. It is torn down with the test.
func Redis(t *testing.T) *cache.Client {
	t.Helper()
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return &cache.Client{Client: client}
}

// Logger returns a logger that discards output unless TEST_VERBOSE is set.
func Logger() *slog.Logger {
	level := slog.LevelError
	if os.Getenv("TEST_VERBOSE") != "" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}
