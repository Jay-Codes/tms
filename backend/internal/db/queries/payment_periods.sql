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
ORDER BY sort_order ASC, days ASC;
