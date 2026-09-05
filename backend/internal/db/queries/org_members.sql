-- name: CreateOrgMember :one
INSERT INTO org_members (org_id, user_id, role)
VALUES (sqlc.arg(org_id), sqlc.arg(user_id), sqlc.arg(role))
RETURNING *;

-- name: ListOrgMembers :many
SELECT m.id, m.org_id, m.user_id, m.role, m.status, m.created_at,
       u.email, u.full_name
FROM org_members m
JOIN users u ON u.id = m.user_id
WHERE m.org_id = sqlc.arg(org_id) AND m.deleted_at IS NULL
ORDER BY m.created_at ASC;

-- name: GetOrgMember :one
SELECT m.id, m.org_id, m.user_id, m.role, m.status, m.created_at,
       u.email, u.full_name
FROM org_members m
JOIN users u ON u.id = m.user_id
WHERE m.org_id = sqlc.arg(org_id) AND m.id = sqlc.arg(id) AND m.deleted_at IS NULL;

-- name: GetOrgMemberByUser :one
SELECT * FROM org_members
WHERE org_id = sqlc.arg(org_id) AND user_id = sqlc.arg(user_id) AND deleted_at IS NULL;

-- guard-exempt: login-time resolution of which org a user belongs to; the
-- authorization subject is the user_id, and the org_id is the result, not an
-- input. Returns at most one row (a user joins one org in MVP).
-- name: GetMembershipForUser :one
SELECT * FROM org_members
WHERE user_id = sqlc.arg(user_id) AND status = 'active' AND deleted_at IS NULL
ORDER BY created_at ASC
LIMIT 1;

-- name: SoftDeleteOrgMember :one
UPDATE org_members SET deleted_at = now()
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- name: CountActiveOwners :one
SELECT count(*) FROM org_members
WHERE org_id = sqlc.arg(org_id) AND role = 'org_owner' AND deleted_at IS NULL;
