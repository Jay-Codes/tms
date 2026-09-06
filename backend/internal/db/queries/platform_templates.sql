-- Phase 14: the platform SMS wording, editable by an admin (SPEC §5.12).
--
-- platform_templates has no org_id: it is the platform's own catalogue, one
-- row per notification kind, and the org's own wording lives in
-- `orgs.settings.notifications.templates` on top of it. The org-scope guard
-- therefore has nothing to check here.

-- name: ListPlatformTemplates :many
SELECT t.kind, t.sw, t.en, t.variables, t.locked, t.version, t.updated_at,
       COALESCE(u.full_name, '')::text AS updated_by
FROM platform_templates t
LEFT JOIN users u ON u.id = t.updated_by_admin_id
ORDER BY t.kind;

-- name: GetPlatformTemplate :one
SELECT t.kind, t.sw, t.en, t.variables, t.locked, t.version, t.updated_at,
       COALESCE(u.full_name, '')::text AS updated_by
FROM platform_templates t
LEFT JOIN users u ON u.id = t.updated_by_admin_id
WHERE t.kind = sqlc.arg(kind);

-- CountPlatformTemplates drives the seed-if-empty on startup: an installation
-- whose catalogue was never seeded gets the code defaults written into the
-- table, so the DB is authoritative from the first boot.
-- name: CountPlatformTemplates :one
SELECT count(*)::bigint FROM platform_templates;

-- SeedPlatformTemplate writes one kind's default, leaving an existing row
-- untouched: seeding is idempotent and never overwrites an admin's wording.
-- name: SeedPlatformTemplate :exec
INSERT INTO platform_templates (kind, sw, en, variables, locked)
VALUES (sqlc.arg(kind), sqlc.arg(sw), sqlc.arg(en), sqlc.arg(variables), sqlc.arg(locked))
ON CONFLICT (kind) DO NOTHING;

-- UpdatePlatformTemplate saves new wording and bumps the version. The previous
-- body is written to platform_template_versions by the caller first, inside the
-- same transaction, so history never loses a step.
-- name: UpdatePlatformTemplate :one
UPDATE platform_templates
SET sw = sqlc.arg(sw), en = sqlc.arg(en),
    updated_by_admin_id = sqlc.narg(admin_user_id),
    version = version + 1
WHERE kind = sqlc.arg(kind)
RETURNING *;

-- name: SetPlatformTemplateLocked :one
UPDATE platform_templates
SET locked = sqlc.arg(locked), updated_by_admin_id = sqlc.narg(admin_user_id)
WHERE kind = sqlc.arg(kind)
RETURNING *;

-- name: InsertPlatformTemplateVersion :exec
INSERT INTO platform_template_versions (kind, version, sw, en, admin_user_id)
VALUES (sqlc.arg(kind), sqlc.arg(version)::int, sqlc.arg(sw), sqlc.arg(en),
        sqlc.narg(admin_user_id))
ON CONFLICT (kind, version) DO NOTHING;

-- name: ListPlatformTemplateVersions :many
SELECT v.version, v.sw, v.en, v.created_at,
       COALESCE(u.full_name, '')::text AS admin_name
FROM platform_template_versions v
LEFT JOIN users u ON u.id = v.admin_user_id
WHERE v.kind = sqlc.arg(kind)
ORDER BY v.version DESC;

-- name: GetPlatformTemplateVersion :one
SELECT version, sw, en, created_at
FROM platform_template_versions
WHERE kind = sqlc.arg(kind) AND version = sqlc.arg(version)::int;

-- ListLockedTemplateKinds is what the landlord's notification settings consult
-- before accepting an override: a locked kind is the platform's wording and
-- stays that way.
-- name: ListLockedTemplateKinds :many
SELECT kind FROM platform_templates WHERE locked ORDER BY kind;
