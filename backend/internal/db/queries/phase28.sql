-- Phase 28 — projections, break-even and ROI (PLAN2 Phase 28).
--
-- The projection is computed in Go (internal/report/projection.go). These
-- queries only read the facts it needs, per property, on the same cash
-- definitions as GET /reports/revenue: collected = non-reversed payments by
-- `paid_at` less rent refunds by `refunded_at` (Phase 22 §22.5), expected =
-- non-waived schedules by `due_date`, expenses = recorded (not voided) by
-- `incurred_on`. Months are calendar months on the Dar es Salaam wall clock.

-- ProjectionProperties is every live property of the org (or the one asked
-- for) with its investment fields and the day it entered the rent book.
-- name: ProjectionProperties :many
SELECT p.id, p.name, p.purchase_price, p.purchase_date, p.current_value,
       (p.created_at AT TIME ZONE 'Africa/Dar_es_Salaam')::date AS created_on
FROM properties p
WHERE p.org_id = sqlc.arg(org_id) AND p.deleted_at IS NULL
  AND (sqlc.narg(property_id)::uuid IS NULL OR p.id = sqlc.narg(property_id)::uuid)
ORDER BY p.name, p.id;

-- ProjectionCollectedMonthly is the whole cash history, per property and
-- month, before refunds (ProjectionRefundedMonthly is subtracted in Go, as
-- phase22_cash.go does for every other cash report).
-- name: ProjectionCollectedMonthly :many
SELECT u.property_id AS property_id,
       date_trunc('month', p.paid_at AT TIME ZONE 'Africa/Dar_es_Salaam')::date AS month,
       COALESCE(sum(p.amount), 0)::bigint AS amount
FROM payments p
JOIN contracts c ON c.id = p.contract_id AND c.org_id = p.org_id
JOIN units u     ON u.id = c.unit_id AND u.org_id = c.org_id
WHERE p.org_id = sqlc.arg(org_id) AND p.deleted_at IS NULL AND p.status <> 'reversed'
  AND p.paid_at < sqlc.arg(to_ts)
  AND (sqlc.narg(property_id)::uuid IS NULL OR u.property_id = sqlc.narg(property_id)::uuid)
GROUP BY 1, 2;

-- name: ProjectionRefundedMonthly :many
SELECT u.property_id AS property_id,
       date_trunc('month', rf.refunded_at AT TIME ZONE 'Africa/Dar_es_Salaam')::date AS month,
       COALESCE(sum(rf.amount), 0)::bigint AS amount
FROM rent_refunds rf
JOIN contracts c ON c.id = rf.contract_id AND c.org_id = rf.org_id
JOIN units u     ON u.id = c.unit_id AND u.org_id = c.org_id
WHERE rf.org_id = sqlc.arg(org_id)
  AND rf.refunded_at < sqlc.arg(to_ts)
  AND (sqlc.narg(property_id)::uuid IS NULL OR u.property_id = sqlc.narg(property_id)::uuid)
GROUP BY 1, 2;

-- ProjectionExpectedMonthly is what fell due, per property and month, over
-- the trailing window: the denominator of the collection rate.
-- name: ProjectionExpectedMonthly :many
SELECT u.property_id AS property_id,
       date_trunc('month', s.due_date)::date AS month,
       COALESCE(sum(s.amount), 0)::bigint AS amount
FROM payment_schedules s
JOIN contracts c ON c.id = s.contract_id AND c.org_id = s.org_id
JOIN units u     ON u.id = c.unit_id AND u.org_id = c.org_id
WHERE s.org_id = sqlc.arg(org_id) AND s.deleted_at IS NULL AND s.status <> 'waived'
  AND s.due_date >= sqlc.arg(from_date) AND s.due_date < sqlc.arg(to_date)
  AND (sqlc.narg(property_id)::uuid IS NULL OR u.property_id = sqlc.narg(property_id)::uuid)
GROUP BY 1, 2;

-- ProjectionExpensesMonthly is the whole expense history, per property,
-- category and month, with the category's capital flag: capital spend is
-- investment, everything else is a running cost.
-- name: ProjectionExpensesMonthly :many
SELECT e.property_id AS property_id,
       e.category_id AS category_id,
       COALESCE(ec.name, '')::text AS category_name,
       COALESCE(ec.is_capital, false)::boolean AS is_capital,
       date_trunc('month', e.incurred_on)::date AS month,
       COALESCE(sum(e.amount), 0)::bigint AS amount
FROM expenses e
LEFT JOIN expense_categories ec ON ec.id = e.category_id AND ec.org_id = e.org_id
WHERE e.org_id = sqlc.arg(org_id) AND e.deleted_at IS NULL AND e.status = 'recorded'
  AND e.incurred_on < sqlc.arg(to_date)
  AND (sqlc.narg(property_id)::uuid IS NULL OR e.property_id = sqlc.narg(property_id)::uuid)
GROUP BY 1, 2, 3, 4, 5;

-- ProjectionScheduledDaily is the known future: every non-waived schedule of
-- a running tenancy (active or expiring) falling due inside the horizon.
-- name: ProjectionScheduledDaily :many
SELECT u.property_id AS property_id,
       s.due_date AS day,
       COALESCE(sum(s.amount), 0)::bigint AS amount
FROM payment_schedules s
JOIN contracts c ON c.id = s.contract_id AND c.org_id = s.org_id
JOIN units u     ON u.id = c.unit_id AND u.org_id = c.org_id
WHERE s.org_id = sqlc.arg(org_id) AND s.deleted_at IS NULL AND s.status <> 'waived'
  AND c.deleted_at IS NULL AND c.status IN ('active', 'expiring')
  AND u.deleted_at IS NULL
  AND s.due_date >= sqlc.arg(from_date) AND s.due_date < sqlc.arg(to_date)
  AND (sqlc.narg(property_id)::uuid IS NULL OR u.property_id = sqlc.narg(property_id)::uuid)
GROUP BY 1, 2;

-- ProjectionUnits is every live unit with what the market would pay for it:
-- its current price (or, without one, the rent of its latest tenancy) and the
-- day its running tenancy, if any, ends.
-- name: ProjectionUnits :many
SELECT u.id, u.property_id, u.status,
       (u.created_at AT TIME ZONE 'Africa/Dar_es_Salaam')::date AS created_on,
       COALESCE(pp.amount, 0)::bigint        AS price_amount,
       COALESCE(pp.period_days, 0)::int      AS price_period_days,
       COALESCE(lc.rent_amount, 0)::bigint   AS last_rent_amount,
       COALESCE(lc.rent_period_days, 0)::int AS last_rent_period_days,
       rc.running_end::date                  AS running_end
FROM units u
LEFT JOIN LATERAL (
    SELECT pl.amount, pl.period_days
    FROM price_plans pl
    WHERE pl.unit_id = u.id AND pl.org_id = u.org_id AND pl.deleted_at IS NULL
      AND pl.effective_from <= CURRENT_DATE
    ORDER BY pl.effective_from DESC, pl.created_at DESC
    LIMIT 1
) pp ON true
LEFT JOIN LATERAL (
    SELECT c.rent_amount, c.rent_period_days
    FROM contracts c
    WHERE c.unit_id = u.id AND c.org_id = u.org_id AND c.deleted_at IS NULL
      AND c.status IN ('active', 'expiring', 'ended', 'terminated')
    ORDER BY c.start_date DESC, c.created_at DESC
    LIMIT 1
) lc ON true
LEFT JOIN LATERAL (
    SELECT max(c.end_date) AS running_end
    FROM contracts c
    WHERE c.unit_id = u.id AND c.org_id = u.org_id AND c.deleted_at IS NULL
      AND c.status IN ('active', 'expiring')
) rc ON true
WHERE u.org_id = sqlc.arg(org_id) AND u.deleted_at IS NULL
  AND (sqlc.narg(property_id)::uuid IS NULL OR u.property_id = sqlc.narg(property_id)::uuid)
ORDER BY u.property_id, u.id;

-- ------------------------------------------------------ saved scenarios --

-- name: ListProjectionScenarios :many
SELECT * FROM projection_scenarios
WHERE org_id = sqlc.arg(org_id)
ORDER BY lower(name), id;

-- name: CountProjectionScenarios :one
SELECT count(*) FROM projection_scenarios WHERE org_id = sqlc.arg(org_id);

-- name: CreateProjectionScenario :one
INSERT INTO projection_scenarios (
    org_id, name, horizon_months, rent_change_pct, occupancy_pct,
    collection_rate_pct, expense_change_pct, created_by_user_id
) VALUES (
    sqlc.arg(org_id), sqlc.arg(name), sqlc.arg(horizon_months), sqlc.arg(rent_change_pct),
    sqlc.narg(occupancy_pct), sqlc.narg(collection_rate_pct), sqlc.arg(expense_change_pct),
    sqlc.arg(created_by_user_id)
)
RETURNING *;

-- name: DeleteProjectionScenario :one
DELETE FROM projection_scenarios
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
RETURNING *;
