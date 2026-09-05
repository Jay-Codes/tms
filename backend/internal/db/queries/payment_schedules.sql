-- payment_schedules are written in one go at activation: every row of the term
-- at the contract's cadence, amounts prorated from the snapshotted rent
-- (SPEC §4). Phase 5 records payments against them; Phase 4 only reads.

-- name: CreatePaymentSchedule :one
INSERT INTO payment_schedules (org_id, contract_id, period_start, period_end, due_date, amount)
VALUES (
    sqlc.arg(org_id), sqlc.arg(contract_id), sqlc.arg(period_start),
    sqlc.arg(period_end), sqlc.arg(due_date), sqlc.arg(amount)
)
RETURNING *;

-- name: ListSchedulesForContract :many
SELECT * FROM payment_schedules
WHERE org_id = sqlc.arg(org_id) AND contract_id = sqlc.arg(contract_id) AND deleted_at IS NULL
ORDER BY period_start, id;

-- ListSchedulesForRenter serves GET /me/schedules across every org the renter
-- rents from, so it is scoped by the renter rather than by an org.
-- guard-exempt: the renter's own schedules, keyed by contracts.renter_user_id across orgs.
-- name: ListSchedulesForRenter :many
SELECT s.id, s.org_id, s.contract_id, s.period_start, s.period_end, s.due_date,
       s.amount, s.status, s.paid_amount, s.created_at,
       c.status AS contract_status,
       u.name AS unit_name, p.name AS property_name, o.name AS org_name
FROM payment_schedules s
JOIN contracts c  ON c.id = s.contract_id AND c.org_id = s.org_id
JOIN units u      ON u.id = c.unit_id AND u.org_id = c.org_id
JOIN properties p ON p.id = u.property_id AND p.org_id = c.org_id
JOIN orgs o       ON o.id = c.org_id
WHERE c.renter_user_id = sqlc.arg(renter_user_id)
  AND s.deleted_at IS NULL AND c.deleted_at IS NULL
ORDER BY s.due_date, s.period_start, s.id;

-- WaiveSchedulesAfter cancels what a terminated contract will never be billed
-- for: rows whose period starts after the termination takes effect. Rows the
-- renter has already lived through stand, paid or not (FLOWS 6.5).
-- name: WaiveSchedulesAfter :many
UPDATE payment_schedules
SET status = 'waived'
WHERE org_id = sqlc.arg(org_id) AND contract_id = sqlc.arg(contract_id)
  AND status IN ('pending', 'overdue', 'partial')
  AND period_start > sqlc.arg(effective_date)
  AND deleted_at IS NULL
RETURNING id;

-- ------------------------------------------------------ Phase 5: payments --

-- LockSchedulesForContract reads every schedule of a contract inside the
-- recording transaction and locks the rows, so two landlords recording at the
-- same moment cannot both allocate against the same outstanding balance.
-- name: LockSchedulesForContract :many
SELECT * FROM payment_schedules
WHERE org_id = sqlc.arg(org_id) AND contract_id = sqlc.arg(contract_id) AND deleted_at IS NULL
ORDER BY due_date, period_start, id
FOR UPDATE;

-- ApplyPaymentToSchedule credits one schedule and flips its status the way
-- API.md describes for a recording: fully covered → `paid`, partly → `partial`.
-- A row that has been waived is never revived by a payment.
-- name: ApplyPaymentToSchedule :one
UPDATE payment_schedules
SET paid_amount = sqlc.arg(paid_amount),
    status = CASE
        WHEN status = 'waived' THEN 'waived'
        WHEN sqlc.arg(paid_amount)::bigint >= amount THEN 'paid'
        ELSE 'partial'
    END
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- UnapplyPaymentFromSchedule is the reversal half: it debits the schedule and
-- recomputes the status from scratch, including the overdue check, because
-- taking money back can push a row past its due date again (API.md Phase 5).
-- name: UnapplyPaymentFromSchedule :one
UPDATE payment_schedules
SET paid_amount = GREATEST(paid_amount - sqlc.arg(delta)::bigint, 0),
    status = CASE
        WHEN status = 'waived' THEN 'waived'
        WHEN GREATEST(paid_amount - sqlc.arg(delta)::bigint, 0) >= amount THEN 'paid'
        WHEN due_date + sqlc.arg(grace_days)::int < CURRENT_DATE THEN 'overdue'
        WHEN GREATEST(paid_amount - sqlc.arg(delta)::bigint, 0) > 0 THEN 'partial'
        ELSE 'pending'
    END
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- ListSchedules is the landlord's schedule board: every row of the org with the
-- contract, unit, property and renter resolved, filtered and cursor-paged by
-- due date (API.md Phase 5).
-- name: ListSchedules :many
SELECT s.*,
       c.renter_user_id, c.status AS contract_status,
       u.name AS unit_name, p.name AS property_name,
       ru.full_name AS renter_name
FROM payment_schedules s
JOIN contracts c  ON c.id = s.contract_id AND c.org_id = s.org_id
JOIN units u      ON u.id = c.unit_id AND u.org_id = c.org_id
JOIN properties p ON p.id = u.property_id AND p.org_id = c.org_id
JOIN users ru     ON ru.id = c.renter_user_id
WHERE s.org_id = sqlc.arg(org_id) AND s.deleted_at IS NULL AND c.deleted_at IS NULL
  AND (sqlc.narg(status)::text IS NULL OR s.status = sqlc.narg(status)::text)
  AND (sqlc.narg(contract_id)::uuid IS NULL OR s.contract_id = sqlc.narg(contract_id)::uuid)
  AND (sqlc.narg(renter_user_id)::uuid IS NULL OR c.renter_user_id = sqlc.narg(renter_user_id)::uuid)
  AND (sqlc.narg(due_from)::date IS NULL OR s.due_date >= sqlc.narg(due_from)::date)
  AND (sqlc.narg(due_to)::date IS NULL OR s.due_date <= sqlc.narg(due_to)::date)
  AND (sqlc.narg(cursor_due)::date IS NULL
       OR (s.due_date, s.id) > (sqlc.narg(cursor_due)::date, sqlc.narg(cursor_id)::uuid))
ORDER BY s.due_date, s.id
LIMIT sqlc.arg(row_limit);

-- GetSchedule resolves one schedule inside its org, used to validate an
-- explicit `schedule_id` on POST /payments.
-- name: GetSchedule :one
SELECT * FROM payment_schedules
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL;
