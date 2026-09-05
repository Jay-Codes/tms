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

-- name: ListPaymentPeriods :many
SELECT * FROM payment_periods
WHERE org_id = sqlc.arg(org_id) AND deleted_at IS NULL
  AND (sqlc.arg(include_inactive)::boolean OR active)
ORDER BY sort_order ASC, days ASC;

-- name: ListActivePaymentPeriods :many
SELECT * FROM payment_periods
WHERE org_id = sqlc.arg(org_id) AND deleted_at IS NULL AND active
ORDER BY sort_order ASC, days ASC;

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
