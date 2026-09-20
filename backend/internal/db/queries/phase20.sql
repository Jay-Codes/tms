-- Phase 20 §20.3 — historical payments backfill.
--
-- `payments.source` (migration 000021) is written by every payment path; the
-- queries here are the two things that reads need on top of it: which source
-- last touched a schedule row (the "imported"/"backfilled" chip) and which rows
-- a backfill has to close.

-- ListLastPaymentSourceForSchedules answers the chip for a page of schedule
-- rows in one round trip, the way ListAllocationsForPayments answers `applied[]`
-- for a page of payments.
--
-- "Last" is the newest live allocation by the payment's own `paid_at`, so a
-- backdated settlement of an old period does not claim to be the newest thing
-- that happened to a row the landlord has since been paid for by hand.
-- Reversed and deleted payments are excluded: their money is no longer on the
-- schedule, so neither is their chip.
-- guard-exempt: the schedule ids were already scoped by the caller (org for the
-- landlord's board, the renter's own contracts for /me/schedules).
-- name: ListLastPaymentSourceForSchedules :many
SELECT DISTINCT ON (a.schedule_id)
       a.schedule_id, pm.source, pm.paid_at
FROM payment_allocations a
JOIN payments pm ON pm.id = a.payment_id
WHERE a.schedule_id = ANY (sqlc.arg(schedule_ids)::uuid[])
  AND pm.status = 'recorded' AND pm.deleted_at IS NULL
ORDER BY a.schedule_id, pm.paid_at DESC, pm.id DESC;

-- LockBackfillSchedules is the set POST /contracts/{id}/backfill works on: every
-- row of the contract due on or before `until`, locked in due-date order so two
-- landlords pressing the button at once cannot settle the same period twice.
--
-- It returns settled rows as well as unsettled ones, because the response has to
-- report how many were skipped, and "skipped" is a fact about rows that were
-- already in range.
-- name: LockBackfillSchedules :many
SELECT * FROM payment_schedules
WHERE org_id = sqlc.arg(org_id) AND contract_id = sqlc.arg(contract_id)
  AND due_date <= sqlc.arg(until) AND deleted_at IS NULL
ORDER BY due_date, period_start, id
FOR UPDATE;

-- WaiveSchedule closes one row without money, the way a termination waives the
-- periods a renter will never live through. It only ever moves an unsettled row,
-- so a re-run of the same backfill is a no-op rather than a second write.
-- name: WaiveSchedule :one
UPDATE payment_schedules SET status = 'waived'
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
  AND status IN ('pending', 'partial', 'overdue') AND deleted_at IS NULL
RETURNING *;

-- FirstSchedulePeriodStart is what the payments import compares a row's
-- `paid_at` against: money dated before the rent book begins has nothing to
-- settle, and the preview says so by pointing at Backfill instead of repeating
-- "exceeds contract balance".
-- name: FirstSchedulePeriodStart :one
SELECT min(period_start)::date AS first_period_start
FROM payment_schedules
WHERE org_id = sqlc.arg(org_id) AND contract_id = sqlc.arg(contract_id)
  AND deleted_at IS NULL;
