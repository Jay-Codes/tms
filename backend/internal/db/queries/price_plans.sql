-- Price history is append-only in practice: a new price is a new row with its
-- own effective_from, and the "current" price is the newest row whose
-- effective_from has arrived (SPEC §4 — active contracts keep their snapshot).

-- name: CreatePricePlan :one
INSERT INTO price_plans (org_id, unit_id, amount, currency, period_days, effective_from, created_by_user_id)
VALUES (
    sqlc.arg(org_id),
    sqlc.arg(unit_id),
    sqlc.arg(amount),
    sqlc.arg(currency),
    sqlc.arg(period_days),
    sqlc.arg(effective_from),
    sqlc.narg(created_by_user_id)
)
RETURNING *;

-- name: ListPricePlans :many
SELECT pl.id, pl.org_id, pl.unit_id, pl.amount, pl.currency, pl.period_days,
       pl.effective_from, pl.created_at, pl.created_by_user_id,
       u.full_name AS created_by_name
FROM price_plans pl
LEFT JOIN users u ON u.id = pl.created_by_user_id
WHERE pl.org_id = sqlc.arg(org_id) AND pl.unit_id = sqlc.arg(unit_id) AND pl.deleted_at IS NULL
ORDER BY pl.effective_from DESC, pl.created_at DESC, pl.id DESC;

-- name: GetCurrentPricePlan :one
SELECT pl.id, pl.org_id, pl.unit_id, pl.amount, pl.currency, pl.period_days,
       pl.effective_from, pl.created_at
FROM price_plans pl
WHERE pl.org_id = sqlc.arg(org_id) AND pl.unit_id = sqlc.arg(unit_id)
  AND pl.deleted_at IS NULL AND pl.effective_from <= CURRENT_DATE
ORDER BY pl.effective_from DESC, pl.created_at DESC
LIMIT 1;
