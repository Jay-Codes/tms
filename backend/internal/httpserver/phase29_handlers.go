// Phase 29 — a backfill may reach back before the contract's start date.
//
// Phase 20 represented an older tenancy by putting the real move-in date on
// the contract. In the field, landlords onboard renters on a fresh contract
// dated the day they joined TMS, and the paper contract that proves the real
// start sits in a drawer. Terminating and re-signing just to move a date is
// friction nobody accepts, so a backfill now takes `from` — the real move-in —
// and writes the missing periods itself, from `from` up to the contract's
// start, at the rent charged back then (`period_amount`, default the
// contract's own per-period rent). The call then settles those up to `until`
// exactly as before; any created period after `until` stays overdue, which is
// the truth until someone says otherwise.
//
// The created periods belong to the batch (`created_by_backfill_id`): undoing
// it reverses their money and removes them, leaving the contract as it was.
package httpserver

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/contract"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/validate"
)

// errBackfillHistoryExists refuses a second `from` on a contract that already
// has periods before its start: the new ones would overlap them.
var errBackfillHistoryExists = errors.New("backfill: contract already has periods before its start")

// errBackfillDryRun rolls back a preview transaction on purpose.
var errBackfillDryRun = errors.New("backfill: dry run")

// backfillHistoryPeriodAmountMax bounds `period_amount` like any other money.
const backfillHistoryPeriodAmountMax = amountMax

// backfillHistoryRows is what `from` would create on this contract: one row
// per payment period from `from` up to the day before the contract starts,
// the last one truncated and prorated. periodAmount 0 means the contract's own
// per-period rent.
func backfillHistoryRows(c sqlc.GetContractRow, from time.Time, periodAmount int64) []contract.Row {
	start := c.StartDate.Time
	days := int(start.Sub(from).Hours() / 24)
	if days <= 0 {
		return nil
	}
	if periodAmount <= 0 {
		periodAmount = backfillDefaultPeriodAmount(c)
	}
	// periodAmount is already one period's rent, so it is its own basis.
	cadence := cadenceOf(c.PaymentPeriodDays, c.PaymentPeriodMonths)
	return contract.GenerateCadence(int(periodAmount), cadence.Days, days, cadence, from, intPtr(c.DueDay))
}

// backfillDefaultPeriodAmount is the contract's rent for one payment period.
func backfillDefaultPeriodAmount(c sqlc.GetContractRow) int64 {
	return contract.RentPerPeriod(c.RentAmount, int(c.RentPeriodDays), int(c.PaymentPeriodDays))
}

// backfillWindow checks `until` and `from` against the contract and the clock.
// It answers field problems (422), errBackfillHistoryExists (409), or a
// database error. `from` is zero when the call does not reach back.
func backfillWindow(
	ctx context.Context, q *sqlc.Queries, c sqlc.GetContractRow, until, from time.Time,
) (validate.Fields, error) {
	f := validate.Fields{}
	start := c.StartDate.Time
	if until.After(todayEAT()) {
		f.Add("until", "must not be in the future")
		return f, nil
	}
	if !from.IsZero() {
		switch {
		case !from.Before(start):
			f.Add("from", "must be before the contract's start date ("+start.Format(dateLayout)+")")
		case from.Before(start.AddDate(0, 0, -contractStartBackstopDays)):
			f.Add("from", "must be at most 10 years before the contract's start date")
		case until.Before(from):
			f.Add("until", "must not be before from ("+from.Format(dateLayout)+")")
		}
		if !f.Empty() {
			return f, nil
		}
		n, err := q.CountSchedulesBeforeStart(ctx, sqlc.CountSchedulesBeforeStartParams{
			OrgID: c.OrgID, ContractID: c.ID,
		})
		if err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, errBackfillHistoryExists
		}
		return f, nil
	}
	// No `from`: the book begins at its first period, which an earlier
	// backfill may have moved before the contract's start.
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
				"); set from to the real move-in date to go further back")
		} else {
			f.Add("until", "must not be before the first period ("+first.Format(dateLayout)+")")
		}
	}
	return f, nil
}

// createBackfillHistory writes the periods `from` asks for, owned by the batch.
func (s *Server) createBackfillHistory(
	ctx context.Context, q *sqlc.Queries, req backfillRequest, batchID pgtype.UUID,
) (int, error) {
	rows := backfillHistoryRows(req.Contract, req.From, req.PeriodAmount)
	for _, r := range rows {
		if _, err := q.CreateBackfillHistorySchedule(ctx, sqlc.CreateBackfillHistoryScheduleParams{
			OrgID: req.OrgID, ContractID: req.Contract.ID,
			PeriodStart: pgtype.Date{Time: r.PeriodStart, Valid: true},
			PeriodEnd:   pgtype.Date{Time: r.PeriodEnd, Valid: true},
			DueDate:     pgtype.Date{Time: r.DueDate, Valid: true}, Amount: r.Amount,
			GraceDays: int32(req.Settings.GraceDays), CreatedByBackfillID: batchID,
		}); err != nil {
			return 0, err
		}
	}
	return len(rows), nil
}
