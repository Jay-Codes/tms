-- name: CreateOrgBranding :one
INSERT INTO org_branding (org_id, display_name)
VALUES (sqlc.arg(org_id), sqlc.arg(display_name))
RETURNING *;

-- name: GetOrgBranding :one
SELECT * FROM org_branding WHERE org_id = sqlc.arg(org_id);

-- name: UpdateOrgBranding :one
UPDATE org_branding
SET display_name         = COALESCE(sqlc.narg(display_name), display_name),
    theme                = COALESCE(sqlc.narg(theme), theme),
    dashboard_prefs      = COALESCE(sqlc.narg(dashboard_prefs), dashboard_prefs),
    document_footer_text = CASE WHEN sqlc.arg(set_footer)::boolean
                                THEN sqlc.narg(document_footer_text)
                                ELSE document_footer_text END
WHERE org_id = sqlc.arg(org_id)
RETURNING *;

-- SetBrandingAsset writes (or clears) one of the two image keys. `which` picks
-- the column so the presign/complete/delete handlers share a single statement.
-- name: SetBrandingAsset :one
UPDATE org_branding
SET logo_object_key       = CASE WHEN sqlc.arg(which)::text = 'logo'
                                 THEN sqlc.narg(object_key) ELSE logo_object_key END,
    letterhead_object_key = CASE WHEN sqlc.arg(which)::text = 'letterhead'
                                 THEN sqlc.narg(object_key) ELSE letterhead_object_key END
WHERE org_id = sqlc.arg(org_id)
RETURNING *;
