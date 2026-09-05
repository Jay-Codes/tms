-- name: CreatePaymentPeriod :one
INSERT INTO payment_periods (org_id, label, days, is_recommended, sort_order)
VALUES (
    sqlc.arg(org_id),
    sqlc.arg(label),
    sqlc.arg(days),
    sqlc.arg(is_recommended),
    sqlc.arg(sort_order)
)
RETURNING *;

-- Recommended first: exactly one period per org carries the badge (migration
-- 000012), and it is the one the landlord wants a renter to see at the top of
-- the list. sort_order then days breaks the rest, as before.
-- name: ListPaymentPeriods :many
SELECT * FROM payment_periods
WHERE org_id = sqlc.arg(org_id) AND deleted_at IS NULL
  AND (sqlc.arg(include_inactive)::boolean OR active)
ORDER BY is_recommended DESC, sort_order ASC, days ASC;

-- name: ListActivePaymentPeriods :many
SELECT * FROM payment_periods
WHERE org_id = sqlc.arg(org_id) AND deleted_at IS NULL AND active
ORDER BY is_recommended DESC, sort_order ASC, days ASC;

-- name: GetPaymentPeriod :one
SELECT * FROM payment_periods
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL;

-- name: UpdatePaymentPeriod :one
UPDATE payment_periods
SET label      = COALESCE(sqlc.narg(label), label),
    days       = COALESCE(sqlc.narg(days), days),
    sort_order = COALESCE(sqlc.narg(sort_order), sort_order),
    active     = COALESCE(sqlc.narg(active), active)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- name: CountActivePaymentPeriods :one
SELECT count(*) FROM payment_periods
WHERE org_id = sqlc.arg(org_id) AND deleted_at IS NULL AND active;

-- name: MaxPaymentPeriodSortOrder :one
SELECT COALESCE(max(sort_order), 0)::int FROM payment_periods
WHERE org_id = sqlc.arg(org_id) AND deleted_at IS NULL;

-- name: GetRecommendedPaymentPeriodByDays :one
SELECT * FROM payment_periods
WHERE org_id = sqlc.arg(org_id) AND days = sqlc.arg(days)
  AND is_recommended AND deleted_at IS NULL
LIMIT 1;

-- name: ReactivatePaymentPeriod :one
UPDATE payment_periods
SET active = true, label = sqlc.arg(label), sort_order = sqlc.arg(sort_order)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- name: ListPaymentPeriodsByIDs :many
SELECT * FROM payment_periods
WHERE org_id = sqlc.arg(org_id) AND id = ANY (sqlc.arg(ids)::uuid[]) AND deleted_at IS NULL;

-- name: ClearRecommendedPaymentPeriod :exec
-- Drops the badge from every live period of the org except the one about to
-- take it. Excluding the target keeps the partial unique index quiet within the
-- transaction and makes the pair idempotent when the badge is already there.
UPDATE payment_periods
SET is_recommended = false
WHERE org_id = sqlc.arg(org_id) AND is_recommended AND deleted_at IS NULL
  AND id <> sqlc.arg(keep_id);

-- name: SetRecommendedPaymentPeriod :one
UPDATE payment_periods
SET is_recommended = true
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- name: GetRecommendedPaymentPeriod :one
SELECT * FROM payment_periods
WHERE org_id = sqlc.arg(org_id) AND is_recommended AND deleted_at IS NULL
LIMIT 1;
