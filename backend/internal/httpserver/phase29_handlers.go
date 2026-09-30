// Phase 29 — a backfill may reach back before the contract's start date.
//
// Phase 30 changed HOW: `from` no longer puts periods on the running contract
// (see phase30_handlers.go, the offline contract). What stays here is the
// shared validation of a backfill and the read side of the batches Phase 29
// already wrote — their `created_by_backfill_id` periods are still removed by
// an undo, and the contract document still leaves them out.
package httpserver

import (
	"context"
	"errors"
	"time"

	"tms/backend/internal/contract"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/validate"
)

// errBackfillDryRun rolls back a preview transaction on purpose.
var errBackfillDryRun = errors.New("backfill: dry run")

// backfillHistoryPeriodAmountMax bounds `period_amount` like any other money.
const backfillHistoryPeriodAmountMax = amountMax

// backfillDefaultPeriodAmount is the contract's rent for one payment period.
func backfillDefaultPeriodAmount(c sqlc.GetContractRow) int64 {
	return contract.RentPerPeriod(c.RentAmount, int(c.RentPeriodDays), int(c.PaymentPeriodDays))
}

// backfillWindow checks `until` against the running contract and the clock for
// a backfill that settles the contract's own periods (no `from`). It answers
// field problems (422) or a database error.
func backfillWindow(
	ctx context.Context, q *sqlc.Queries, c sqlc.GetContractRow, until time.Time,
) (validate.Fields, error) {
	f := validate.Fields{}
	start := c.StartDate.Time
	if until.After(todayEAT()) {
		f.Add("until", "must not be in the future")
		return f, nil
	}
	// The book begins at its first period, which a Phase 29 backfill may have
	// moved before the contract's start.
	first := start
	got, err := q.FirstSchedulePeriodStart(ctx, sqlc.FirstSchedulePeriodStartParams{
		OrgID: c.OrgID, ContractID: c.ID,
	})
	if err != nil && !isNoRows(err) {
		return nil, err
	}
	if got.Valid && got.Time.Before(first) {
		first = got.Time
	}
	if until.Before(first) {
		if first.Equal(start) {
			f.Add("until", "must not be before the contract's start date ("+start.Format(dateLayout)+
				"); give from to record an offline contract for the time before it")
		} else {
			f.Add("until", "must not be before the first period ("+first.Format(dateLayout)+")")
		}
	}
	return f, nil
}
