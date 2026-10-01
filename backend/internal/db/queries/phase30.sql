-- Phase 30 — offline contracts: the ended, never-signed contract a backfill
-- writes for a tenancy that ran outside TMS.

-- FindOverlappingContract is the offline stretch's clash check: any other live
-- contract of the unit whose span meets [from, to_excl). Spans are half-open
-- (end_date is the day after the last covered day, as the generator's EndDate),
-- and a terminated contract is cut at its effective date. Drafts and unsigned
-- ones (an amendment waiting to take over included) cover nothing yet.
-- name: FindOverlappingContract :one
SELECT c.id, c.status, c.is_offline, c.start_date,
       (CASE WHEN c.status = 'terminated' AND c.termination_effective_date IS NOT NULL
             THEN LEAST(c.end_date, c.termination_effective_date + 1)
             ELSE c.end_date END)::date AS end_date
FROM contracts c
WHERE c.org_id = sqlc.arg(org_id) AND c.unit_id = sqlc.arg(unit_id)
  AND c.deleted_at IS NULL
  AND c.status IN ('active', 'expiring', 'ended', 'terminated')
  AND c.start_date < sqlc.arg(to_excl)::date
  AND (CASE WHEN c.status = 'terminated' AND c.termination_effective_date IS NOT NULL
            THEN LEAST(c.end_date, c.termination_effective_date + 1)
            ELSE c.end_date END) > sqlc.arg(from_date)::date
ORDER BY c.start_date
LIMIT 1;

-- FindRunningContractForUnit is the tenancy running on a unit, whoever the
-- renter: an offline stretch has to stop the day before it starts.
-- name: FindRunningContractForUnit :one
SELECT id FROM contracts
WHERE org_id = sqlc.arg(org_id) AND unit_id = sqlc.arg(unit_id) AND deleted_at IS NULL
  AND status IN ('active', 'expiring')
ORDER BY start_date DESC, created_at DESC
LIMIT 1;

-- SoftDeleteOfflineContract is the undo's last step for an offline contract.
-- name: SoftDeleteOfflineContract :execrows
UPDATE contracts SET deleted_at = now()
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND is_offline
  AND deleted_at IS NULL;

-- CreateOfflineSchedule writes one period of an offline contract, owned by the
-- batch. It starts `pending`; the settlement in the same transaction closes it.
-- name: CreateOfflineSchedule :one
INSERT INTO payment_schedules (
    org_id, contract_id, period_start, period_end, due_date, amount, status, created_by_backfill_id
)
VALUES (
    sqlc.arg(org_id), sqlc.arg(contract_id), sqlc.arg(period_start), sqlc.arg(period_end),
    sqlc.arg(due_date), sqlc.arg(amount), 'pending', sqlc.arg(created_by_backfill_id)
)
RETURNING *;

-- FindNextContractStart is Phase 32's floor: the first contract of the unit,
-- offline or not, that starts after `from` and on or before the stretch's
-- last day. The offline stretch then ends the day before it — the contract
-- already in TMS wins its own start date.
-- name: FindNextContractStart :one
SELECT min(c.start_date)::date AS start_date
FROM contracts c
WHERE c.org_id = sqlc.arg(org_id) AND c.unit_id = sqlc.arg(unit_id)
  AND c.deleted_at IS NULL
  AND c.status IN ('active', 'expiring', 'ended', 'terminated')
  AND c.start_date > sqlc.arg(from_date)::date
  AND c.start_date <= sqlc.arg(last_day)::date;
