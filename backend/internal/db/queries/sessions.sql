-- sessions are keyed by an opaque token hash, not by org: the org_id column is
-- part of the session payload (what the principal is scoped to), not a filter.

-- name: CreateSession :one
INSERT INTO sessions (token_hash, user_id, org_id, audience, role, ip, user_agent, expires_at)
VALUES (
    sqlc.arg(token_hash),
    sqlc.arg(user_id),
    sqlc.narg(org_id),
    sqlc.arg(audience),
    sqlc.narg(role),
    sqlc.narg(ip),
    sqlc.narg(user_agent),
    sqlc.arg(expires_at)
)
RETURNING *;

-- name: GetSessionByHash :one
SELECT * FROM sessions
WHERE token_hash = sqlc.arg(token_hash)
  AND revoked_at IS NULL
  AND expires_at > now();

-- name: RevokeSession :exec
UPDATE sessions SET revoked_at = now()
WHERE token_hash = sqlc.arg(token_hash) AND revoked_at IS NULL;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at < now() - INTERVAL '7 days';
