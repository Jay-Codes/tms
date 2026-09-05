-- A link request is a renter's application for a unit (SPEC §5.4). It is
-- org-scoped like everything else, but it is also read by the renter who made
-- it: those queries filter by renter_user_id instead, which is the renter's
-- own isolation boundary (a renter may hold requests with several orgs).

-- name: CreateLinkRequest :one
INSERT INTO unit_link_requests (
    org_id, unit_id, renter_user_id, status, payment_period_id,
    term_days, start_date, end_date, accepted_terms_at, decided_at, decided_by_user_id
)
VALUES (
    sqlc.arg(org_id), sqlc.arg(unit_id), sqlc.arg(renter_user_id), sqlc.arg(status),
    sqlc.arg(payment_period_id), sqlc.arg(term_days), sqlc.arg(start_date), sqlc.arg(end_date),
    now(), sqlc.narg(decided_at), sqlc.narg(decided_by_user_id)
)
RETURNING *;

-- CountPendingLinkRequest backs the duplicate check. It is keyed by the
-- renter's own id across orgs (the unit already pins the org), so it is
-- exempt from the org_id guard.
-- guard-exempt: keyed by (unit_id, renter_user_id); the unit determines the org.
-- name: CountPendingLinkRequest :one
SELECT count(*) FROM unit_link_requests
WHERE unit_id = sqlc.arg(unit_id) AND renter_user_id = sqlc.arg(renter_user_id)
  AND status = 'pending' AND deleted_at IS NULL;

-- name: GetLinkRequest :one
SELECT lr.id, lr.org_id, lr.unit_id, lr.renter_user_id, lr.status,
       lr.payment_period_id, lr.term_days, lr.start_date, lr.end_date,
       lr.rejection_reason, lr.decided_at, lr.decided_by_user_id,
       lr.accepted_terms_at, lr.created_at, lr.updated_at,
       u.name AS unit_name, u.unit_code, u.status AS unit_status,
       p.name AS property_name,
       o.name AS org_name, o.slug AS org_slug,
       ru.phone AS renter_phone, ru.email AS renter_email, ru.full_name AS renter_name,
       pp.label AS period_label, COALESCE(pp.days, 0)::int AS period_days,
       COALESCE(pl.amount, 0)::bigint AS price_amount, COALESCE(pl.period_days, 0)::int AS price_period_days,
       (pl.amount IS NOT NULL)::boolean AS has_price,
       COALESCE(rp.kyc_status, 'incomplete')::text AS kyc_status,
       (rp.kyc_doc_object_key IS NOT NULL)::boolean AS kyc_doc_uploaded
FROM unit_link_requests lr
JOIN units u      ON u.id = lr.unit_id AND u.org_id = lr.org_id
JOIN properties p ON p.id = u.property_id AND p.org_id = lr.org_id
JOIN orgs o       ON o.id = lr.org_id
JOIN users ru     ON ru.id = lr.renter_user_id
LEFT JOIN payment_periods pp ON pp.id = lr.payment_period_id AND pp.org_id = lr.org_id
LEFT JOIN renter_profiles rp ON rp.user_id = lr.renter_user_id AND rp.deleted_at IS NULL
LEFT JOIN LATERAL (
    SELECT pr.amount, pr.period_days FROM price_plans pr
    WHERE pr.unit_id = u.id AND pr.org_id = lr.org_id AND pr.deleted_at IS NULL
      AND pr.effective_from <= CURRENT_DATE
    ORDER BY pr.effective_from DESC, pr.created_at DESC LIMIT 1
) pl ON true
WHERE lr.id = sqlc.arg(id) AND lr.deleted_at IS NULL
  AND lr.org_id = COALESCE(sqlc.narg(org_id)::uuid, lr.org_id)
  AND lr.renter_user_id = COALESCE(sqlc.narg(renter_user_id)::uuid, lr.renter_user_id);

-- ListLinkRequests serves both inboxes. The landlord passes org_id and leaves
-- renter_user_id NULL; the renter passes their own user id and leaves org_id
-- NULL. Exactly one of the two is always set (the handlers own that), so a row
-- is never returned to a caller outside its own boundary.
-- guard-exempt: dual-scoped — org_id for the landlord inbox, renter_user_id for the renter's own list; the handler always supplies one.
-- name: ListLinkRequests :many
SELECT lr.id, lr.org_id, lr.unit_id, lr.renter_user_id, lr.status,
       lr.payment_period_id, lr.term_days, lr.start_date, lr.end_date,
       lr.rejection_reason, lr.decided_at, lr.decided_by_user_id,
       lr.accepted_terms_at, lr.created_at, lr.updated_at,
       u.name AS unit_name, u.unit_code, u.status AS unit_status,
       p.name AS property_name,
       o.name AS org_name, o.slug AS org_slug,
       ru.phone AS renter_phone, ru.email AS renter_email, ru.full_name AS renter_name,
       pp.label AS period_label, COALESCE(pp.days, 0)::int AS period_days,
       COALESCE(pl.amount, 0)::bigint AS price_amount, COALESCE(pl.period_days, 0)::int AS price_period_days,
       (pl.amount IS NOT NULL)::boolean AS has_price,
       COALESCE(rp.kyc_status, 'incomplete')::text AS kyc_status,
       (rp.kyc_doc_object_key IS NOT NULL)::boolean AS kyc_doc_uploaded
FROM unit_link_requests lr
JOIN units u      ON u.id = lr.unit_id AND u.org_id = lr.org_id
JOIN properties p ON p.id = u.property_id AND p.org_id = lr.org_id
JOIN orgs o       ON o.id = lr.org_id
JOIN users ru     ON ru.id = lr.renter_user_id
LEFT JOIN payment_periods pp ON pp.id = lr.payment_period_id AND pp.org_id = lr.org_id
LEFT JOIN renter_profiles rp ON rp.user_id = lr.renter_user_id AND rp.deleted_at IS NULL
LEFT JOIN LATERAL (
    SELECT pr.amount, pr.period_days FROM price_plans pr
    WHERE pr.unit_id = u.id AND pr.org_id = lr.org_id AND pr.deleted_at IS NULL
      AND pr.effective_from <= CURRENT_DATE
    ORDER BY pr.effective_from DESC, pr.created_at DESC LIMIT 1
) pl ON true
WHERE lr.deleted_at IS NULL
  AND (sqlc.narg(org_id)::uuid IS NULL OR lr.org_id = sqlc.narg(org_id)::uuid)
  AND (sqlc.narg(renter_user_id)::uuid IS NULL OR lr.renter_user_id = sqlc.narg(renter_user_id)::uuid)
  AND (sqlc.narg(unit_id)::uuid IS NULL OR lr.unit_id = sqlc.narg(unit_id)::uuid)
  AND (sqlc.narg(status)::text IS NULL OR lr.status = sqlc.narg(status)::text)
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (lr.created_at, lr.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY lr.created_at DESC, lr.id DESC
LIMIT sqlc.arg(row_limit);

-- name: DecideLinkRequest :one
UPDATE unit_link_requests
SET status             = sqlc.arg(status),
    rejection_reason   = sqlc.narg(rejection_reason),
    decided_at         = now(),
    decided_by_user_id = sqlc.narg(decided_by_user_id)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
  AND status = 'pending' AND deleted_at IS NULL
RETURNING *;

-- CancelLinkRequest is the renter withdrawing their own pending request, so it
-- is keyed by renter_user_id rather than by org.
-- guard-exempt: the renter's own request, keyed by (id, renter_user_id).
-- name: CancelLinkRequest :one
UPDATE unit_link_requests
SET status = 'cancelled', decided_at = now()
WHERE id = sqlc.arg(id) AND renter_user_id = sqlc.arg(renter_user_id)
  AND status = 'pending' AND deleted_at IS NULL
RETURNING *;
