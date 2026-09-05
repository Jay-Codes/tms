-- Phase 7 reports (SPEC §5.9, API.md Phase 7). Every figure on a report page is
-- one SQL aggregate over the whole org: a dashboard that walked contracts in Go
-- would be N+1 by construction, and these are the queries a landlord opens
-- first thing every morning.
--
-- Definitions, fixed by API.md and used identically by all three reports:
--   expected  = payment_schedules with due_date inside the window, excluding
--               `waived` rows (a waived period was never billed).
--   collected = payments with paid_at inside the window whose status is not
--               `reversed` (a correction takes the money back out).
--   outstanding / overdue amounts are always `amount - paid_amount`, floored at
--               zero, over the unsettled statuses.

-- name: ReportAssets :one
SELECT
    (SELECT count(*) FROM properties p
     WHERE p.org_id = sqlc.arg(org_id) AND p.deleted_at IS NULL)::bigint AS properties,
    count(*)::bigint                                          AS units,
    count(*) FILTER (WHERE u.status = 'occupied')::bigint     AS occupied,
    count(*) FILTER (WHERE u.status = 'vacant')::bigint       AS vacant,
    count(*) FILTER (WHERE u.status = 'maintenance')::bigint  AS maintenance,
    count(*) FILTER (WHERE u.status = 'unlisted')::bigint     AS unlisted
FROM units u
WHERE u.org_id = sqlc.arg(org_id) AND u.deleted_at IS NULL;

-- ReportContractCounts also carries the active-renter headcount: a renter is
-- "active" when they hold a running tenancy, and counting them here rather than
-- over users keeps the number consistent with the contract figures beside it.
-- name: ReportContractCounts :one
SELECT
    count(*) FILTER (WHERE status = 'active')::bigint            AS active,
    count(*) FILTER (WHERE status = 'expiring')::bigint          AS expiring,
    count(*) FILTER (WHERE status = 'pending_signature')::bigint AS pending_signature,
    count(DISTINCT renter_user_id)
        FILTER (WHERE status IN ('active', 'expiring'))::bigint  AS active_renters
FROM contracts
WHERE org_id = sqlc.arg(org_id) AND deleted_at IS NULL;

-- name: ReportSchedulePeriod :one
SELECT
    COALESCE(sum(amount) FILTER (WHERE status <> 'waived'), 0)::bigint AS expected,
    COALESCE(sum(GREATEST(amount - paid_amount, 0))
        FILTER (WHERE status IN ('pending', 'partial', 'overdue')), 0)::bigint AS outstanding,
    count(*) FILTER (WHERE status = 'overdue')::bigint AS overdue_count,
    COALESCE(sum(GREATEST(amount - paid_amount, 0))
        FILTER (WHERE status = 'overdue'), 0)::bigint AS overdue_amount
FROM payment_schedules
WHERE org_id = sqlc.arg(org_id) AND deleted_at IS NULL
  AND due_date >= sqlc.arg(from_date) AND due_date <= sqlc.arg(to_date);

-- name: ReportCollectedPeriod :one
SELECT COALESCE(sum(amount), 0)::bigint AS collected
FROM payments
WHERE org_id = sqlc.arg(org_id) AND deleted_at IS NULL AND status <> 'reversed'
  AND paid_at >= sqlc.arg(from_ts) AND paid_at < sqlc.arg(to_ts);

-- ReportVacantUnits is the "money not being made" panel. `days_vacant` counts
-- from the end of the last tenancy the unit had, or from the day the unit was
-- created when it has never been let.
-- name: ReportVacantUnits :many
SELECT u.id AS unit_id, u.name AS unit_name, pr.name AS property_name,
       GREATEST(CURRENT_DATE - COALESCE(
           (SELECT max(c.end_date) FROM contracts c
            WHERE c.org_id = u.org_id AND c.unit_id = u.id AND c.deleted_at IS NULL
              AND c.status IN ('ended', 'terminated')),
           u.created_at::date), 0)::int AS days_vacant
FROM units u
JOIN properties pr ON pr.id = u.property_id AND pr.org_id = u.org_id
WHERE u.org_id = sqlc.arg(org_id) AND u.deleted_at IS NULL AND u.status = 'vacant'
ORDER BY days_vacant DESC, u.name
LIMIT sqlc.arg(row_limit);

-- ---------------------------------------------- GET /reports/payment-status --

-- The per-renter report is four constant-count queries, joined in Go by
-- contract id: the tenancies, their balances, their next unsettled due date and
-- their last payment. Four round trips for any number of renters.

-- name: ReportTenancies :many
SELECT c.id AS contract_id, c.renter_user_id,
       ru.full_name AS renter_name, ru.phone AS renter_phone,
       u.name AS unit_name, pr.id AS property_id, pr.name AS property_name
FROM contracts c
JOIN units u       ON u.id = c.unit_id AND u.org_id = c.org_id
JOIN properties pr ON pr.id = u.property_id AND pr.org_id = c.org_id
JOIN users ru      ON ru.id = c.renter_user_id
WHERE c.org_id = sqlc.arg(org_id) AND c.deleted_at IS NULL
  AND c.status IN ('active', 'expiring')
  AND (sqlc.narg(property_id)::uuid IS NULL OR pr.id = sqlc.narg(property_id)::uuid)
ORDER BY pr.name, u.name, c.id;

-- name: ReportContractBalances :many
SELECT s.contract_id,
       COALESCE(sum(GREATEST(s.amount - s.paid_amount, 0))
           FILTER (WHERE s.status IN ('pending', 'partial', 'overdue')), 0)::bigint AS outstanding,
       COALESCE(sum(GREATEST(s.amount - s.paid_amount, 0))
           FILTER (WHERE s.status = 'overdue'), 0)::bigint AS overdue_amount,
       count(*) FILTER (WHERE s.status = 'overdue')::bigint AS overdue_count,
       count(*) FILTER (WHERE s.status = 'partial')::bigint AS partial_count,
       count(*) FILTER (WHERE s.status = 'pending')::bigint AS pending_count
FROM payment_schedules s
WHERE s.org_id = sqlc.arg(org_id) AND s.deleted_at IS NULL
GROUP BY s.contract_id;

-- name: ReportNextDue :many
SELECT DISTINCT ON (s.contract_id)
       s.contract_id, s.due_date,
       GREATEST(s.amount - s.paid_amount, 0)::bigint AS amount
FROM payment_schedules s
WHERE s.org_id = sqlc.arg(org_id) AND s.deleted_at IS NULL
  AND s.status IN ('pending', 'partial', 'overdue')
ORDER BY s.contract_id, s.due_date, s.id;

-- name: ReportLastPayments :many
SELECT p.contract_id, max(p.paid_at)::timestamptz AS last_payment_at
FROM payments p
WHERE p.org_id = sqlc.arg(org_id) AND p.deleted_at IS NULL AND p.status <> 'reversed'
GROUP BY p.contract_id;

-- ------------------------------------------------ GET /reports/collections --

-- One row per non-empty bucket; the handler fills the gaps so a quiet month
-- still appears as a zero rather than as a hole in the series.

-- name: ReportCollectionsExpected :many
SELECT date_trunc(sqlc.arg(bucket)::text, s.due_date::timestamptz)::date AS bucket_start,
       COALESCE(sum(s.amount), 0)::bigint AS expected
FROM payment_schedules s
WHERE s.org_id = sqlc.arg(org_id) AND s.deleted_at IS NULL AND s.status <> 'waived'
  AND s.due_date >= sqlc.arg(from_date) AND s.due_date <= sqlc.arg(to_date)
GROUP BY 1
ORDER BY 1;

-- name: ReportCollectionsCollected :many
-- `paid_at` is an instant; the bucket it belongs to is the one on the org's own
-- wall clock, so it is shifted into EAT before truncation. Otherwise a payment
-- taken at 22:00 on the last day of a month lands in the month before.
SELECT date_trunc(sqlc.arg(bucket)::text, p.paid_at AT TIME ZONE 'Africa/Dar_es_Salaam')::date AS bucket_start,
       COALESCE(sum(p.amount), 0)::bigint AS collected
FROM payments p
WHERE p.org_id = sqlc.arg(org_id) AND p.deleted_at IS NULL AND p.status <> 'reversed'
  AND p.paid_at >= sqlc.arg(from_ts) AND p.paid_at < sqlc.arg(to_ts)
GROUP BY 1
ORDER BY 1;
