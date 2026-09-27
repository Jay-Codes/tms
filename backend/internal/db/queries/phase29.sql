-- Phase 29 — a backfill that reaches back before the contract's start date
-- writes the missing periods itself, and its undo removes them again.

-- CreateBackfillHistorySchedule writes one period that predates the contract.
-- Its status is what the book would say had it existed all along: overdue
-- once past due and grace, pending otherwise. The backfill then settles the
-- ones up to `until` in the same transaction.
-- name: CreateBackfillHistorySchedule :one
INSERT INTO payment_schedules (
    org_id, contract_id, period_start, period_end, due_date, amount, status, created_by_backfill_id
)
VALUES (
    sqlc.arg(org_id), sqlc.arg(contract_id), sqlc.arg(period_start), sqlc.arg(period_end),
    sqlc.arg(due_date), sqlc.arg(amount),
    CASE WHEN sqlc.arg(due_date)::date + sqlc.arg(grace_days)::int < CURRENT_DATE
         THEN 'overdue' ELSE 'pending' END,
    sqlc.arg(created_by_backfill_id)
)
RETURNING *;

-- CountSchedulesBeforeStart says whether a contract already has periods before
-- its start date — written by an earlier backfill. A second `from` would
-- overlap them, so it is refused; `until` alone can still settle them.
-- name: CountSchedulesBeforeStart :one
SELECT count(*)::int AS n
FROM payment_schedules s
JOIN contracts c ON c.id = s.contract_id
WHERE s.org_id = sqlc.arg(org_id) AND s.contract_id = sqlc.arg(contract_id)
  AND s.period_start < c.start_date AND s.deleted_at IS NULL;

-- DeleteBackfillCreatedSchedules is the undo's last step: the periods the
-- batch wrote go away with it. Soft delete, so the reversed payments that
-- pointed at them still resolve.
-- name: DeleteBackfillCreatedSchedules :execrows
UPDATE payment_schedules SET deleted_at = now()
WHERE org_id = sqlc.arg(org_id) AND created_by_backfill_id = sqlc.arg(created_by_backfill_id)
  AND deleted_at IS NULL;
