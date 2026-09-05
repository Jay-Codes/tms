-- orgs is the tenancy root: it is addressed by its own primary key, which is
-- the org scope. Rows here are never listed across orgs outside admin_*.sql.

-- name: CreateOrg :one
INSERT INTO orgs (name, slug, settings)
VALUES (sqlc.arg(name), sqlc.arg(slug), sqlc.arg(settings))
RETURNING *;

-- name: GetOrg :one
SELECT * FROM orgs WHERE id = sqlc.arg(id) AND deleted_at IS NULL;

-- name: GetOrgBySlug :one
SELECT * FROM orgs WHERE slug = sqlc.arg(slug) AND deleted_at IS NULL;

-- name: OrgSlugExists :one
SELECT EXISTS (SELECT 1 FROM orgs WHERE slug = sqlc.arg(slug));

-- name: UpdateOrg :one
UPDATE orgs
SET name     = COALESCE(sqlc.narg(name), name),
    settings = COALESCE(sqlc.narg(settings), settings)
WHERE id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;
