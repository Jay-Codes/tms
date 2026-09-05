-- audit_log is append-only (enforced by triggers in 000002): INSERT + SELECT
-- only. Reads are always org-scoped; the platform admin view lives in
-- admin_audit.sql.

-- name: InsertAuditLog :one
INSERT INTO audit_log (org_id, actor_user_id, action, entity_type, entity_id, before, after, ip, user_agent)
VALUES (
    sqlc.narg(org_id),
    sqlc.narg(actor_user_id),
    sqlc.arg(action),
    sqlc.arg(entity_type),
    sqlc.narg(entity_id),
    sqlc.narg(before),
    sqlc.narg(after),
    sqlc.narg(ip),
    sqlc.narg(user_agent)
)
RETURNING *;

-- name: ListAuditLog :many
SELECT a.id, a.org_id, a.actor_user_id, a.action, a.entity_type, a.entity_id,
       a.before, a.after, a.ip, a.user_agent, a.at,
       u.full_name AS actor_name
FROM audit_log a
LEFT JOIN users u ON u.id = a.actor_user_id
WHERE a.org_id = sqlc.arg(org_id)
  AND (sqlc.narg(entity_type)::text IS NULL OR a.entity_type = sqlc.narg(entity_type)::text)
  AND (sqlc.narg(actor_user_id)::uuid IS NULL OR a.actor_user_id = sqlc.narg(actor_user_id)::uuid)
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR a.at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR a.at <= sqlc.narg(to_at)::timestamptz)
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (a.at, a.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY a.at DESC, a.id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetAuditLogEntry :one
SELECT a.id, a.org_id, a.actor_user_id, a.action, a.entity_type, a.entity_id,
       a.before, a.after, a.ip, a.user_agent, a.at,
       u.full_name AS actor_name
FROM audit_log a
LEFT JOIN users u ON u.id = a.actor_user_id
WHERE a.org_id = sqlc.arg(org_id) AND a.id = sqlc.arg(id);
