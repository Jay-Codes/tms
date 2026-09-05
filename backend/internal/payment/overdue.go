package payment

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
)

// OverdueInterval is how often cmd/api sweeps for schedules whose due date has
// passed. Hourly, like the contract lifecycle: a demo must never wait a day to
// see a status change, and the statement is idempotent so extra runs cost
// nothing.
const OverdueInterval = time.Hour

// FlipOverdue moves unsettled schedules past their due date (plus the org's
// grace period) to `overdue`.
//
// Pass a zero orgID to sweep the whole platform — what the ticker and
// `POST /admin/jobs/overdue` do. Pass an org's id to sweep only that tenant,
// which is what the org-scoped reads do before answering, so a landlord or a
// renter never sees a stale `pending` on a schedule that lapsed an hour ago
// (API.md Phase 5).
//
// The grace period is each org's own `settings.grace_days`, defaulting to 0
// when the setting is absent. The statement selects only `pending`/`partial`,
// so running it twice changes nothing the second time.
func FlipOverdue(ctx context.Context, q *sqlc.Queries, orgID pgtype.UUID) (int, error) {
	if q == nil {
		return 0, fmt.Errorf("payment: overdue sweep needs a database")
	}
	rows, err := q.FlipOverdueSchedules(ctx, orgID)
	if err != nil {
		return 0, fmt.Errorf("payment: flip overdue: %w", err)
	}
	return len(rows), nil
}

// RunOverdueTicker runs the sweep once at startup and then every
// OverdueInterval until ctx is cancelled. A failed sweep is logged, never
// fatal: the next tick tries again, and the on-demand flip on every read keeps
// the API's own answers correct in the meantime.
func RunOverdueTicker(ctx context.Context, pool *db.Pool, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	if pool == nil {
		logger.Warn("overdue job not started: postgres unavailable")
		return
	}
	q := sqlc.New(pool)
	run := func() {
		flipped, err := FlipOverdue(ctx, q, pgtype.UUID{})
		if err != nil {
			logger.Error("overdue sweep failed", "error", err)
			return
		}
		if flipped > 0 {
			logger.Info("overdue sweep", "flipped", flipped)
		}
	}
	run()

	ticker := time.NewTicker(OverdueInterval)
	defer ticker.Stop()
	logger.Info("overdue job started", "interval", OverdueInterval.String())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
