-- name: CreateOrgBranding :one
INSERT INTO org_branding (org_id, display_name)
VALUES (sqlc.arg(org_id), sqlc.arg(display_name))
RETURNING *;

-- name: GetOrgBranding :one
SELECT * FROM org_branding WHERE org_id = sqlc.arg(org_id);
