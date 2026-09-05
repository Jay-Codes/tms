-- Platform-admin queries. Files named admin_*.sql are exempt from the
-- org-scope guard (see internal/db/orgscope_guard_test.go): these endpoints
-- deliberately cross orgs and are reachable only behind auth.RequireAdmin.

-- name: AdminListOrgs :many
SELECT * FROM orgs
WHERE deleted_at IS NULL
ORDER BY created_at DESC
LIMIT sqlc.arg(row_limit);

-- name: AdminCountOrgs :one
SELECT count(*) FROM orgs WHERE deleted_at IS NULL;
