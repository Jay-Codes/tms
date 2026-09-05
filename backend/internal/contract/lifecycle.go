package contract

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
)

// ExpiringNoticeDays is how long before its end date a running contract is
// flagged `expiring`, prompting the landlord to renew or let it lapse
// (SPEC §5.5, FLOWS 6.4).
const ExpiringNoticeDays = 30

// LifecycleInterval is how often cmd/api runs the sweep. It is hourly rather
// than nightly so a demo or a UAT session never waits a day to see a contract
// change state; the statements are idempotent, so the extra runs cost nothing.
const LifecycleInterval = time.Hour

// LifecycleResult reports what one sweep changed.
type LifecycleResult struct {
	Expiring int `json:"expiring"`
	Ended    int `json:"ended"`
	Freed    int `json:"freed"`
}

// RunLifecycle advances contracts whose dates have caught up with them:
// `active` within 30 days of its end becomes `expiring`, and anything at or
// past its end date becomes `ended` with its unit returned to the vacancy board
// (unless another contract still runs on that unit).
//
// It is a platform-wide sweep — the timestamps it acts on are the same in every
// org — so it crosses orgs by design (queries live in admin_contract_lifecycle
// .sql). It is safe to run repeatedly: each statement selects on the status it
// is moving away from.
func RunLifecycle(ctx context.Context, pool *db.Pool) (LifecycleResult, error) {
	var out LifecycleResult
	if pool == nil {
		return out, fmt.Errorf("contract: lifecycle needs a database")
	}
	q := sqlc.New(pool)

	// Ending first: a contract that has already lapsed should not be flagged
	// `expiring` on its way past.
	ended, err := q.EndLapsedContracts(ctx)
	if err != nil {
		return out, fmt.Errorf("contract: end lapsed: %w", err)
	}
	out.Ended = len(ended)

	expiring, err := q.FlagExpiringContracts(ctx, ExpiringNoticeDays)
	if err != nil {
		return out, fmt.Errorf("contract: flag expiring: %w", err)
	}
	out.Expiring = len(expiring)

	if len(ended) > 0 {
		unitIDs := make([]pgtype.UUID, 0, len(ended))
		for _, row := range ended {
			unitIDs = append(unitIDs, row.UnitID)
		}
		freed, err := q.FreeUnitsWithoutLiveContract(ctx, unitIDs)
		if err != nil {
			return out, fmt.Errorf("contract: free units: %w", err)
		}
		out.Freed = len(freed)
	}
	return out, nil
}

// RunLifecycleTicker runs the sweep once at startup and then every
// LifecycleInterval until ctx is cancelled. A failed sweep is logged, never
// fatal: the next tick tries again, and the API keeps serving in the meantime.
func RunLifecycleTicker(ctx context.Context, pool *db.Pool, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	if pool == nil {
		logger.Warn("contract lifecycle job not started: postgres unavailable")
		return
	}
	run := func() {
		res, err := RunLifecycle(ctx, pool)
		if err != nil {
			logger.Error("contract lifecycle sweep failed", "error", err)
			return
		}
		if res.Expiring+res.Ended+res.Freed > 0 {
			logger.Info("contract lifecycle sweep",
				"expiring", res.Expiring, "ended", res.Ended, "units_freed", res.Freed)
		}
	}
	run()

	ticker := time.NewTicker(LifecycleInterval)
	defer ticker.Stop()
	logger.Info("contract lifecycle job started", "interval", LifecycleInterval.String())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
