-- Phase 24 — payment corrections and landlord notices.

-- PossibleDuplicates: live payments on the same contract for the same amount
-- within three days, or anywhere in the org with the same reference in the
-- last 90 days.
-- name: PossibleDuplicates :many
SELECT p.id, p.contract_id, p.amount, p.paid_at, p.method, p.reference, p.created_at
FROM payments p
WHERE p.org_id = sqlc.arg(org_id) AND p.deleted_at IS NULL AND p.status <> 'reversed'
  AND (
    (p.contract_id = sqlc.arg(contract_id) AND p.amount = sqlc.arg(amount)
     AND p.paid_at BETWEEN sqlc.arg(paid_at)::timestamptz - interval '3 days'
                       AND sqlc.arg(paid_at)::timestamptz + interval '3 days')
    OR (sqlc.narg(reference)::text IS NOT NULL
        AND lower(p.reference) = lower(sqlc.narg(reference)::text)
        AND p.paid_at >= sqlc.arg(paid_at)::timestamptz - interval '90 days')
  )
ORDER BY p.paid_at DESC
LIMIT 5;

-- name: PaymentByIdempotencyKey :one
SELECT id FROM payments WHERE org_id = sqlc.arg(org_id) AND idempotency_key = sqlc.arg(key);

-- name: SetPaymentExtras :exec
UPDATE payments SET idempotency_key = sqlc.narg(idempotency_key), corrects_payment_id = sqlc.narg(corrects_payment_id)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id);

-- ------------------------------------------------------ landlord notices --

-- name: CreateInboxItem :one
INSERT INTO org_inbox (org_id, kind, title, body, entity_type, entity_id, link, actor_user_id)
VALUES (sqlc.arg(org_id), sqlc.arg(kind), sqlc.arg(title), sqlc.arg(body), sqlc.narg(entity_type),
        sqlc.narg(entity_id), sqlc.narg(link), sqlc.narg(actor_user_id))
RETURNING id;

-- name: ListInbox :many
SELECT n.*, (r.read_at IS NOT NULL)::bool AS read
FROM org_inbox n
LEFT JOIN org_inbox_reads r ON r.notification_id = n.id AND r.user_id = sqlc.arg(user_id)
WHERE n.org_id = sqlc.arg(org_id)
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (n.created_at, n.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY n.created_at DESC, n.id DESC
LIMIT sqlc.arg(row_limit);

-- name: CountUnreadInbox :one
SELECT count(*)::bigint FROM org_inbox n
WHERE n.org_id = sqlc.arg(org_id)
  AND NOT EXISTS (SELECT 1 FROM org_inbox_reads r
                  WHERE r.notification_id = n.id AND r.user_id = sqlc.arg(user_id));

-- name: MarkInboxRead :exec
INSERT INTO org_inbox_reads (notification_id, user_id, org_id)
SELECT n.id, sqlc.arg(user_id), n.org_id FROM org_inbox n
WHERE n.org_id = sqlc.arg(org_id)
  AND (sqlc.arg(all_ids)::bool OR n.id = ANY(sqlc.arg(ids)::uuid[]))
ON CONFLICT DO NOTHING;
