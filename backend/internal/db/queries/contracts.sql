-- A contract is the agreement between an org and a renter over one unit. Like
-- link requests it has two boundaries: the org owns it, and the renter who
-- signed it may read their own. Queries take one or the other, never neither.
--
-- The row shape resolves, per contract: the unit and its property, the renter's
-- account, the chosen cadence, and a summary of the materialised
-- payment_schedules (empty until activation, when they are all written at once).

-- name: CreateContract :one
INSERT INTO contracts (
    org_id, unit_id, renter_user_id, template_id, terms_snapshot_html,
    rent_amount, rent_period_days, payment_period_id, payment_period_days,
    term_days, start_date, end_date, due_day, status, snapshot_hash, link_request_id
)
VALUES (
    sqlc.arg(org_id), sqlc.arg(unit_id), sqlc.arg(renter_user_id), sqlc.narg(template_id),
    sqlc.arg(terms_snapshot_html), sqlc.arg(rent_amount), sqlc.arg(rent_period_days),
    sqlc.narg(payment_period_id), sqlc.arg(payment_period_days), sqlc.arg(term_days),
    sqlc.arg(start_date), sqlc.arg(end_date), sqlc.narg(due_day), sqlc.arg(status),
    sqlc.arg(snapshot_hash), sqlc.narg(link_request_id)
)
RETURNING *;

-- guard-exempt: dual-scoped — org_id for the landlord, renter_user_id for the renter's own contract; the handler always supplies one.
-- name: GetContract :one
SELECT c.*,
       u.name AS unit_name, u.unit_code, u.status AS unit_status,
       p.name AS property_name, p.location_text AS property_location_text,
       o.name AS org_name, o.slug AS org_slug,
       ru.full_name AS renter_name, ru.phone AS renter_phone, ru.email AS renter_email,
       pp.label AS period_label,
       COALESCE(sm.cnt, 0)::bigint       AS schedule_count,
       COALESCE(sm.total, 0)::bigint     AS schedule_total,
       COALESCE(sm.paid_count, 0)::bigint    AS schedule_paid_count,
       COALESCE(sm.overdue_count, 0)::bigint AS schedule_overdue_count,
       nd.due_date AS next_due_date,
       COALESCE(nd.amount, 0)::bigint AS next_due_amount
FROM contracts c
JOIN units u      ON u.id = c.unit_id AND u.org_id = c.org_id
JOIN properties p ON p.id = u.property_id AND p.org_id = c.org_id
JOIN orgs o       ON o.id = c.org_id
JOIN users ru     ON ru.id = c.renter_user_id
LEFT JOIN payment_periods pp ON pp.id = c.payment_period_id AND pp.org_id = c.org_id
LEFT JOIN LATERAL (
    SELECT count(*) AS cnt, sum(s.amount) AS total,
           count(*) FILTER (WHERE s.status = 'paid') AS paid_count,
           count(*) FILTER (WHERE s.status = 'overdue') AS overdue_count
    FROM payment_schedules s
    WHERE s.contract_id = c.id AND s.org_id = c.org_id AND s.deleted_at IS NULL
) sm ON true
LEFT JOIN LATERAL (
    SELECT s.due_date, s.amount FROM payment_schedules s
    WHERE s.contract_id = c.id AND s.org_id = c.org_id AND s.deleted_at IS NULL
      AND s.status IN ('pending', 'partial', 'overdue')
    ORDER BY s.due_date, s.period_start LIMIT 1
) nd ON true
WHERE c.id = sqlc.arg(id) AND c.deleted_at IS NULL
  AND c.org_id = COALESCE(sqlc.narg(org_id)::uuid, c.org_id)
  AND c.renter_user_id = COALESCE(sqlc.narg(renter_user_id)::uuid, c.renter_user_id);

-- guard-exempt: dual-scoped — org_id for the landlord's list, renter_user_id for GET /me/contracts; the handler always supplies one.
-- name: ListContracts :many
SELECT c.*,
       u.name AS unit_name, u.unit_code, u.status AS unit_status,
       p.name AS property_name, p.location_text AS property_location_text,
       o.name AS org_name, o.slug AS org_slug,
       ru.full_name AS renter_name, ru.phone AS renter_phone, ru.email AS renter_email,
       pp.label AS period_label,
       COALESCE(sm.cnt, 0)::bigint       AS schedule_count,
       COALESCE(sm.total, 0)::bigint     AS schedule_total,
       COALESCE(sm.paid_count, 0)::bigint    AS schedule_paid_count,
       COALESCE(sm.overdue_count, 0)::bigint AS schedule_overdue_count,
       nd.due_date AS next_due_date,
       COALESCE(nd.amount, 0)::bigint AS next_due_amount
FROM contracts c
JOIN units u      ON u.id = c.unit_id AND u.org_id = c.org_id
JOIN properties p ON p.id = u.property_id AND p.org_id = c.org_id
JOIN orgs o       ON o.id = c.org_id
JOIN users ru     ON ru.id = c.renter_user_id
LEFT JOIN payment_periods pp ON pp.id = c.payment_period_id AND pp.org_id = c.org_id
LEFT JOIN LATERAL (
    SELECT count(*) AS cnt, sum(s.amount) AS total,
           count(*) FILTER (WHERE s.status = 'paid') AS paid_count,
           count(*) FILTER (WHERE s.status = 'overdue') AS overdue_count
    FROM payment_schedules s
    WHERE s.contract_id = c.id AND s.org_id = c.org_id AND s.deleted_at IS NULL
) sm ON true
LEFT JOIN LATERAL (
    SELECT s.due_date, s.amount FROM payment_schedules s
    WHERE s.contract_id = c.id AND s.org_id = c.org_id AND s.deleted_at IS NULL
      AND s.status IN ('pending', 'partial', 'overdue')
    ORDER BY s.due_date, s.period_start LIMIT 1
) nd ON true
WHERE c.deleted_at IS NULL
  AND (sqlc.narg(org_id)::uuid IS NULL OR c.org_id = sqlc.narg(org_id)::uuid)
  AND (sqlc.narg(renter_user_id)::uuid IS NULL OR c.renter_user_id = sqlc.narg(renter_user_id)::uuid)
  AND (sqlc.narg(unit_id)::uuid IS NULL OR c.unit_id = sqlc.narg(unit_id)::uuid)
  AND (sqlc.narg(status)::text IS NULL OR c.status = sqlc.narg(status)::text)
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (c.created_at, c.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg(row_limit);

-- CountLiveContractsForUnit backs the 409 on POST /contracts: a unit may carry
-- only one contract that is pending signature or running.
-- name: CountLiveContractsForUnit :one
SELECT count(*) FROM contracts
WHERE org_id = sqlc.arg(org_id) AND unit_id = sqlc.arg(unit_id)
  AND status IN ('pending_signature', 'active', 'expiring') AND deleted_at IS NULL;

-- CountOtherActiveContractsForUnit decides whether a termination frees the
-- unit: it stays occupied if some other contract still runs on it.
-- name: CountOtherActiveContractsForUnit :one
SELECT count(*) FROM contracts
WHERE org_id = sqlc.arg(org_id) AND unit_id = sqlc.arg(unit_id)
  AND id <> sqlc.arg(exclude_id)
  AND status IN ('active', 'expiring') AND deleted_at IS NULL;

-- GetContractForLinkRequest backs the approval backfill: approving a request
-- that already has a contract must not create a second one.
-- name: GetContractForLinkRequest :one
SELECT id FROM contracts
WHERE org_id = sqlc.arg(org_id) AND link_request_id = sqlc.arg(link_request_id)
  AND deleted_at IS NULL
LIMIT 1;

-- name: ActivateContract :one
UPDATE contracts
SET status = 'active', activated_at = now()
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
  AND status = 'pending_signature' AND deleted_at IS NULL
RETURNING *;

-- name: TerminateContract :one
UPDATE contracts
SET status = 'terminated', terminated_at = now(),
    termination_reason = sqlc.arg(termination_reason),
    termination_effective_date = sqlc.arg(termination_effective_date)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
  AND status IN ('pending_signature', 'active', 'expiring') AND deleted_at IS NULL
RETURNING *;

-- RenterKnownToOrg is the check behind the 404 on an unknown renter: an org may
-- only write a contract for someone who has applied to it or already rents from
-- it (SPEC §5.4 — the directory is exactly this relationship).
-- name: RenterKnownToOrg :one
SELECT EXISTS (
    SELECT 1 FROM unit_link_requests lr
    WHERE lr.org_id = sqlc.arg(org_id) AND lr.renter_user_id = sqlc.arg(renter_user_id)
      AND lr.deleted_at IS NULL
    UNION ALL
    SELECT 1 FROM contracts c
    WHERE c.org_id = sqlc.arg(org_id) AND c.renter_user_id = sqlc.arg(renter_user_id)
      AND c.deleted_at IS NULL
);
