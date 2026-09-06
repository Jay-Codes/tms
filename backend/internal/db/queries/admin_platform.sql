-- Phase 7 platform-admin queries (SPEC §5.10, API.md Phase 7). Files named
-- admin_*.sql are exempt from the org-scope guard: these statements cross orgs
-- by design and are reachable only behind auth.RequireAdmin (`tms_a`).

-- AdminListOrgsPage is the admin org list: one row per tenant with the owner,
-- the four headline counts and 30-day SMS volume — all as correlated
-- aggregates, so a hundred orgs is still one round trip.
-- name: AdminListOrgsPage :many
SELECT o.id, o.name, o.slug, o.status, o.created_at, o.suspended_at, o.suspended_reason,
       COALESCE((SELECT ou.full_name FROM org_members m
                 JOIN users ou ON ou.id = m.user_id
                 WHERE m.org_id = o.id AND m.role = 'org_owner' AND m.deleted_at IS NULL
                 ORDER BY m.created_at, m.id LIMIT 1), '')::text AS owner_name,
       COALESCE((SELECT ou2.email FROM org_members m2
                 JOIN users ou2 ON ou2.id = m2.user_id
                 WHERE m2.org_id = o.id AND m2.role = 'org_owner' AND m2.deleted_at IS NULL
                 ORDER BY m2.created_at, m2.id LIMIT 1), '')::text AS owner_email,
       (SELECT count(*) FROM properties p
        WHERE p.org_id = o.id AND p.deleted_at IS NULL)::bigint AS properties,
       (SELECT count(*) FROM units u
        WHERE u.org_id = o.id AND u.deleted_at IS NULL)::bigint AS units,
       (SELECT count(DISTINCT c.renter_user_id) FROM contracts c
        WHERE c.org_id = o.id AND c.deleted_at IS NULL
          AND c.status IN ('active', 'expiring'))::bigint AS renters,
       (SELECT count(*) FROM contracts c2
        WHERE c2.org_id = o.id AND c2.deleted_at IS NULL
          AND c2.status IN ('active', 'expiring'))::bigint AS active_contracts,
       (SELECT count(*) FROM notification_log n
        WHERE n.org_id = o.id AND n.status = 'sent'
          AND n.created_at >= now() - INTERVAL '30 days')::bigint AS sms_sent_30d,
       (SELECT count(*) FROM notification_log n2
        WHERE n2.org_id = o.id AND n2.status = 'failed'
          AND n2.created_at >= now() - INTERVAL '30 days')::bigint AS sms_failed_30d
FROM orgs o
WHERE o.deleted_at IS NULL
  AND (sqlc.narg(status)::text IS NULL OR o.status = sqlc.narg(status)::text)
  AND (sqlc.narg(q)::text IS NULL
       OR o.name ILIKE '%' || sqlc.narg(q)::text || '%'
       OR o.slug ILIKE '%' || sqlc.narg(q)::text || '%')
  AND (sqlc.narg(org_id)::uuid IS NULL OR o.id = sqlc.narg(org_id)::uuid)
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (o.created_at, o.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY o.created_at DESC, o.id DESC
LIMIT sqlc.arg(row_limit);

-- name: AdminListOrgMembers :many
SELECT m.id, m.user_id, m.role, m.status, m.created_at,
       u.full_name, u.email
FROM org_members m
JOIN users u ON u.id = m.user_id
WHERE m.org_id = sqlc.arg(org_id) AND m.deleted_at IS NULL
ORDER BY m.created_at, m.id;

-- name: AdminSuspendOrg :one
UPDATE orgs
SET status = 'suspended', suspended_at = now(), suspended_reason = sqlc.arg(reason)
WHERE id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- name: AdminActivateOrg :one
UPDATE orgs
SET status = 'active', suspended_at = NULL, suspended_reason = NULL
WHERE id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- AdminMetrics is the platform health panel: every counter in one statement,
-- because the admin dashboard polls it.
-- name: AdminMetrics :one
SELECT
    (SELECT count(*) FROM orgs WHERE deleted_at IS NULL)::bigint AS orgs_total,
    (SELECT count(*) FROM orgs WHERE deleted_at IS NULL AND status = 'active')::bigint AS orgs_active,
    (SELECT count(*) FROM orgs WHERE deleted_at IS NULL AND status = 'suspended')::bigint AS orgs_suspended,
    (SELECT count(*) FROM users WHERE deleted_at IS NULL AND kind = 'renter')::bigint AS renters_total,
    (SELECT count(*) FROM units WHERE deleted_at IS NULL)::bigint AS units_total,
    (SELECT count(*) FROM units WHERE deleted_at IS NULL AND status = 'occupied')::bigint AS units_occupied,
    (SELECT count(*) FROM contracts WHERE deleted_at IS NULL
      AND status IN ('active', 'expiring'))::bigint AS contracts_active,
    (SELECT count(*) FROM notification_log WHERE status = 'sent'
      AND created_at >= now() - INTERVAL '24 hours')::bigint AS sms_sent_24h,
    (SELECT count(*) FROM notification_log WHERE status = 'failed'
      AND created_at >= now() - INTERVAL '24 hours')::bigint AS sms_failed_24h,
    (SELECT count(*) FROM notification_log
      WHERE status IN ('queued', 'sending'))::bigint AS sms_queued,
    (SELECT count(*) FROM payments WHERE deleted_at IS NULL AND status <> 'reversed'
      AND paid_at >= now() - INTERVAL '30 days')::bigint AS payments_recorded_30d,
    (SELECT COALESCE(sum(amount), 0) FROM payments WHERE deleted_at IS NULL AND status <> 'reversed'
      AND paid_at >= now() - INTERVAL '30 days')::bigint AS payments_amount_30d;

-- AdminListAuditLog is the cross-org audit search. `q` is a case-insensitive
-- substring of the action or the entity type — the two fields an operator
-- actually remembers ("suspend", "payment", "contract").
-- name: AdminListAuditLog :many
SELECT a.id, a.org_id, a.actor_user_id, a.action, a.entity_type, a.entity_id,
       a.before, a.after, a.ip, a.user_agent, a.at,
       u.full_name AS actor_name,
       COALESCE(o.name, '')::text AS org_name
FROM audit_log a
LEFT JOIN users u ON u.id = a.actor_user_id
LEFT JOIN orgs o  ON o.id = a.org_id
WHERE (sqlc.narg(org_id)::uuid IS NULL OR a.org_id = sqlc.narg(org_id)::uuid)
  AND (sqlc.narg(actor_user_id)::uuid IS NULL OR a.actor_user_id = sqlc.narg(actor_user_id)::uuid)
  AND (sqlc.narg(entity_type)::text IS NULL OR a.entity_type = sqlc.narg(entity_type)::text)
  AND (sqlc.narg(entity_id)::uuid IS NULL OR a.entity_id = sqlc.narg(entity_id)::uuid)
  AND (sqlc.narg(q)::text IS NULL
       OR a.action ILIKE '%' || sqlc.narg(q)::text || '%'
       OR a.entity_type ILIKE '%' || sqlc.narg(q)::text || '%')
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR a.at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR a.at <= sqlc.narg(to_at)::timestamptz)
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (a.at, a.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY a.at DESC, a.id DESC
LIMIT sqlc.arg(row_limit);

-- AdminJobLastRuns answers GET /admin/jobs from the audit rows the jobs already
-- write, rather than from a second `job_runs` table that could disagree with
-- them (DECISIONS.md).
-- name: AdminJobLastRuns :many
SELECT DISTINCT ON (a.action) a.action, a.at, a.after
FROM audit_log a
WHERE a.action = ANY (sqlc.arg(actions)::text[])
ORDER BY a.action, a.at DESC, a.id DESC;

-- AdminGetOrgStatus is the suspension check RequireOrg runs when the Redis flag
-- is cold: one primary-key lookup, cached for a few minutes afterwards.
-- name: AdminGetOrgStatus :one
SELECT status FROM orgs WHERE id = sqlc.arg(id) AND deleted_at IS NULL;


-- AdminSMSMetrics is the Phase 14 block of GET /admin/metrics: what the
-- platform's orgs have spent today, how many are under their own watermark,
-- and how much mail is held for want of credit.
-- name: AdminSMSMetrics :one
SELECT
    (SELECT COALESCE(-sum(delta), 0) FROM sms_credit_ledger
      WHERE reason = 'debit' AND created_at >= date_trunc('day', now()))::bigint
      AS credits_used_today,
    (SELECT count(*) FROM org_sms_credits c
      JOIN orgs o ON o.id = c.org_id AND o.deleted_at IS NULL
      WHERE c.balance < c.low_watermark)::bigint AS orgs_under_watermark,
    (SELECT count(*) FROM notification_log
      WHERE status = 'held_no_credit')::bigint AS held_total;
