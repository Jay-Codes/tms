-- A payment is money the landlord received outside the system and recorded
-- against a contract (SPEC §5.6, FLOWS 7). It carries the schedule it was aimed
-- at; payment_allocations carry every schedule it actually settled, because an
-- overpayment rolls forward.
--
-- Like contracts, payments have two readers: the org owns them, and the renter
-- may read their own. The dual-scoped queries take one or the other, never
-- neither.

-- `import_batch_id` is NULL for money a landlord keys in and set for a row that
-- arrived on a CSV import (Phase 16 §16.2), which is what puts the "imported"
-- chip on a ledger row and what the 24 h undo walks.
-- name: CreatePayment :one
INSERT INTO payments (
    org_id, contract_id, schedule_id, amount, method, reference, paid_at,
    recorded_by_user_id, note, import_batch_id
)
VALUES (
    sqlc.arg(org_id), sqlc.arg(contract_id), sqlc.narg(schedule_id), sqlc.arg(amount),
    sqlc.arg(method), sqlc.narg(reference), sqlc.arg(paid_at),
    sqlc.narg(recorded_by_user_id), sqlc.narg(note), sqlc.narg(import_batch_id)
)
RETURNING *;

-- name: CreatePaymentAllocation :one
INSERT INTO payment_allocations (org_id, payment_id, schedule_id, amount)
VALUES (sqlc.arg(org_id), sqlc.arg(payment_id), sqlc.arg(schedule_id), sqlc.arg(amount))
RETURNING *;

-- ReversePayment is the correction path: it only ever moves a `recorded`
-- payment, so a second reversal finds no row and the handler answers 409.
-- name: ReversePayment :one
UPDATE payments
SET status = 'reversed', reversed_at = now(),
    reversal_reason = sqlc.arg(reversal_reason),
    reversed_by_user_id = sqlc.narg(reversed_by_user_id)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
  AND status = 'recorded' AND deleted_at IS NULL
RETURNING *;

-- guard-exempt: dual-scoped — org_id for the landlord, renter_user_id for the renter's own payment; the handler always supplies one.
-- name: GetPayment :one
SELECT p.*,
       c.renter_user_id, c.unit_id,
       u.name AS unit_name, pr.name AS property_name,
       ru.full_name AS renter_name,
       rb.full_name AS recorded_by_name
FROM payments p
JOIN contracts c   ON c.id = p.contract_id AND c.org_id = p.org_id
JOIN units u       ON u.id = c.unit_id AND u.org_id = c.org_id
JOIN properties pr ON pr.id = u.property_id AND pr.org_id = c.org_id
JOIN users ru      ON ru.id = c.renter_user_id
LEFT JOIN users rb ON rb.id = p.recorded_by_user_id
WHERE p.id = sqlc.arg(id) AND p.deleted_at IS NULL
  AND p.org_id = COALESCE(sqlc.narg(org_id)::uuid, p.org_id)
  AND c.renter_user_id = COALESCE(sqlc.narg(renter_user_id)::uuid, c.renter_user_id);

-- guard-exempt: dual-scoped — org_id for GET /payments, renter_user_id for GET /me/payments; the handler always supplies one.
-- name: ListPayments :many
SELECT p.*,
       c.renter_user_id, c.unit_id,
       u.name AS unit_name, pr.name AS property_name,
       ru.full_name AS renter_name,
       rb.full_name AS recorded_by_name
FROM payments p
JOIN contracts c   ON c.id = p.contract_id AND c.org_id = p.org_id
JOIN units u       ON u.id = c.unit_id AND u.org_id = c.org_id
JOIN properties pr ON pr.id = u.property_id AND pr.org_id = c.org_id
JOIN users ru      ON ru.id = c.renter_user_id
LEFT JOIN users rb ON rb.id = p.recorded_by_user_id
WHERE p.deleted_at IS NULL
  AND (sqlc.narg(org_id)::uuid IS NULL OR p.org_id = sqlc.narg(org_id)::uuid)
  AND (sqlc.narg(renter_user_id)::uuid IS NULL OR c.renter_user_id = sqlc.narg(renter_user_id)::uuid)
  AND (sqlc.narg(contract_id)::uuid IS NULL OR p.contract_id = sqlc.narg(contract_id)::uuid)
  AND (sqlc.narg(method)::text IS NULL OR p.method = sqlc.narg(method)::text)
  AND (sqlc.narg(paid_from)::timestamptz IS NULL OR p.paid_at >= sqlc.narg(paid_from)::timestamptz)
  AND (sqlc.narg(paid_to)::timestamptz IS NULL OR p.paid_at <= sqlc.narg(paid_to)::timestamptz)
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (p.paid_at, p.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY p.paid_at DESC, p.id DESC
LIMIT sqlc.arg(row_limit);

-- ListAllocationsForPayments fills the `applied[]` array of every payment in a
-- listing in one round trip. Allocation rows are all written inside one
-- transaction, so they share a created_at to the microsecond; the order that
-- means anything is the one the money walked in — by due date.
-- name: ListAllocationsForPayments :many
SELECT a.payment_id, a.schedule_id, a.amount, s.due_date
FROM payment_allocations a
JOIN payment_schedules s ON s.id = a.schedule_id AND s.org_id = a.org_id
WHERE a.org_id = sqlc.arg(org_id) AND a.payment_id = ANY (sqlc.arg(payment_ids)::uuid[])
ORDER BY a.payment_id, s.due_date, a.schedule_id;

-- ListAllocationsForPaymentsAnyOrg serves GET /me/payments, which spans every
-- org the renter rents from; the payment ids it is given were already scoped to
-- that renter's own contracts.
-- guard-exempt: the payment ids were already scoped to the renter's own contracts.
-- name: ListAllocationsForPaymentsAnyOrg :many
SELECT a.payment_id, a.schedule_id, a.amount, s.due_date
FROM payment_allocations a
JOIN payment_schedules s ON s.id = a.schedule_id AND s.org_id = a.org_id
WHERE a.payment_id = ANY (sqlc.arg(payment_ids)::uuid[])
ORDER BY a.payment_id, s.due_date, a.schedule_id;
