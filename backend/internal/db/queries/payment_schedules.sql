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
