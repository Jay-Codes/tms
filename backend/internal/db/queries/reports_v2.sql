-- Phase 11 reports (SPEC §5.9, PLAN2 Phase 11): the revenue series, its
-- per-property breakdown, and the occupancy series.
--
-- Definitions, shared with the Phase 7 reports so the two never disagree:
--   collected = payments with `paid_at` inside the window whose status is not
--               `reversed`, bucketed on the org's own wall clock.
--   expected  = payment_schedules with `due_date` inside the window, excluding
--               `waived` rows — which is also how a terminated tenancy's
--               remaining periods drop out (FLOWS 6.5 waives them).
--   expenses  = expenses with `incurred_on` inside the window and status
--               `recorded` (a voided row is a correction, not a cost).
--
-- Every window here is half-open `[from, to)`, matching internal/period.
--
-- The series are read at **day** grain and bucketed in Go rather than with
-- `date_trunc`. A custom range may start on the 12th, and internal/period's
-- buckets then start on the 12th too; `date_trunc` would align them to the 1st
-- and the two would silently disagree about which bucket a row belongs to. A
-- five-year window is at most ~1830 rows per series, which is cheaper to carry
-- than a bucketing bug.
--
-- `property_id` is filtered through EXISTS rather than a join, so the plain
-- (unfiltered) call still walks the `(org_id, paid_at)` index alone.

-- ------------------------------------------------- GET /reports/revenue --

-- name: RevenueCollectedDaily :many
SELECT (p.paid_at AT TIME ZONE 'Africa/Dar_es_Salaam')::date AS day,
       COALESCE(sum(p.amount), 0)::bigint AS amount
FROM payments p
WHERE p.org_id = sqlc.arg(org_id) AND p.deleted_at IS NULL AND p.status <> 'reversed'
  AND p.paid_at >= sqlc.arg(from_ts) AND p.paid_at < sqlc.arg(to_ts)
  AND (sqlc.narg(property_id)::uuid IS NULL OR EXISTS (
        SELECT 1 FROM contracts c
        JOIN units u ON u.id = c.unit_id AND u.org_id = c.org_id
        WHERE c.id = p.contract_id AND c.org_id = p.org_id
          AND u.property_id = sqlc.narg(property_id)::uuid))
GROUP BY 1
ORDER BY 1;

-- name: RevenueExpectedDaily :many
SELECT s.due_date AS day,
       COALESCE(sum(s.amount), 0)::bigint AS amount
FROM payment_schedules s
WHERE s.org_id = sqlc.arg(org_id) AND s.deleted_at IS NULL AND s.status <> 'waived'
  AND s.due_date >= sqlc.arg(from_date) AND s.due_date < sqlc.arg(to_date)
  AND (sqlc.narg(property_id)::uuid IS NULL OR EXISTS (
        SELECT 1 FROM contracts c
        JOIN units u ON u.id = c.unit_id AND u.org_id = c.org_id
        WHERE c.id = s.contract_id AND c.org_id = s.org_id
          AND u.property_id = sqlc.narg(property_id)::uuid))
GROUP BY 1
ORDER BY 1;

-- name: RevenueExpensesDaily :many
SELECT e.incurred_on AS day,
       COALESCE(sum(e.amount), 0)::bigint AS amount
FROM expenses e
WHERE e.org_id = sqlc.arg(org_id) AND e.deleted_at IS NULL AND e.status = 'recorded'
  AND e.incurred_on >= sqlc.arg(from_date) AND e.incurred_on < sqlc.arg(to_date)
  AND (sqlc.narg(property_id)::uuid IS NULL OR e.property_id = sqlc.narg(property_id)::uuid)
GROUP BY 1
ORDER BY 1;

-- --------------------------------- GET /reports/revenue?group_by=property --

-- The three breakdowns key on property_id; the handler zero-fills them against
-- the org's live properties, so a property that earned nothing this month is a
-- row of zeroes rather than a gap.

-- name: RevenueCollectedByProperty :many
SELECT u.property_id AS property_id,
       COALESCE(sum(p.amount), 0)::bigint AS amount
FROM payments p
JOIN contracts c ON c.id = p.contract_id AND c.org_id = p.org_id
JOIN units u     ON u.id = c.unit_id AND u.org_id = c.org_id
WHERE p.org_id = sqlc.arg(org_id) AND p.deleted_at IS NULL AND p.status <> 'reversed'
  AND p.paid_at >= sqlc.arg(from_ts) AND p.paid_at < sqlc.arg(to_ts)
  AND (sqlc.narg(property_id)::uuid IS NULL OR u.property_id = sqlc.narg(property_id)::uuid)
GROUP BY 1;

-- name: RevenueExpectedByProperty :many
SELECT u.property_id AS property_id,
       COALESCE(sum(s.amount), 0)::bigint AS amount
FROM payment_schedules s
JOIN contracts c ON c.id = s.contract_id AND c.org_id = s.org_id
JOIN units u     ON u.id = c.unit_id AND u.org_id = c.org_id
WHERE s.org_id = sqlc.arg(org_id) AND s.deleted_at IS NULL AND s.status <> 'waived'
  AND s.due_date >= sqlc.arg(from_date) AND s.due_date < sqlc.arg(to_date)
  AND (sqlc.narg(property_id)::uuid IS NULL OR u.property_id = sqlc.narg(property_id)::uuid)
GROUP BY 1;

-- name: RevenueExpensesByProperty :many
SELECT e.property_id AS property_id,
       COALESCE(sum(e.amount), 0)::bigint AS amount
FROM expenses e
WHERE e.org_id = sqlc.arg(org_id) AND e.deleted_at IS NULL AND e.status = 'recorded'
  AND e.incurred_on >= sqlc.arg(from_date) AND e.incurred_on < sqlc.arg(to_date)
  AND (sqlc.narg(property_id)::uuid IS NULL OR e.property_id = sqlc.narg(property_id)::uuid)
GROUP BY 1;

-- ReportPropertyNames is the zero-fill skeleton: every live property of the
-- org, in the order the breakdown falls back to when two properties tie.
-- name: ReportPropertyNames :many
SELECT p.id, p.name
FROM properties p
WHERE p.org_id = sqlc.arg(org_id) AND p.deleted_at IS NULL
  AND (sqlc.narg(property_id)::uuid IS NULL OR p.id = sqlc.narg(property_id)::uuid)
ORDER BY p.name, p.id;

-- ----------------------------------------------- GET /reports/occupancy --

-- OccupancyContractSpans is every tenancy that could have covered any day of
-- the window, as a half-open `[start_date, end_date)` span.
--
-- `draft` and `pending_signature` contracts are excluded: nobody has moved in.
-- `ended` and `terminated` ones are included, because occupancy is a history —
-- a unit that was let in March was let in March whatever happened since. A
-- terminated tenancy is occupied through its effective date inclusive, which is
-- the same day its remaining schedules stop being waived (FLOWS 6.5), so the
-- exclusive end is the day after.
-- name: OccupancyContractSpans :many
SELECT c.unit_id,
       c.start_date,
       (CASE WHEN c.status = 'terminated' AND c.termination_effective_date IS NOT NULL
             THEN LEAST(c.end_date, c.termination_effective_date + 1)
             ELSE c.end_date END)::date AS end_date
FROM contracts c
JOIN units u ON u.id = c.unit_id AND u.org_id = c.org_id
WHERE c.org_id = sqlc.arg(org_id) AND c.deleted_at IS NULL
  AND c.status IN ('active', 'expiring', 'ended', 'terminated')
  AND c.start_date < sqlc.arg(to_date)
  AND (sqlc.narg(property_id)::uuid IS NULL OR u.property_id = sqlc.narg(property_id)::uuid);

-- OccupancyUnitDates is the denominator's raw material: when each live unit
-- came into existence, so a bucket before a unit was created does not count it.
-- name: OccupancyUnitDates :many
SELECT u.id, u.created_at::date AS created_on
FROM units u
WHERE u.org_id = sqlc.arg(org_id) AND u.deleted_at IS NULL
  AND (sqlc.narg(property_id)::uuid IS NULL OR u.property_id = sqlc.narg(property_id)::uuid);
