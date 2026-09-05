-- Phase 12 theming. One row per org (the org_id is the primary key), holding
-- whichever of "a preset", "an explicit token set" and "a font" the landlord
-- has chosen. Resolution of that into the theme an app renders lives in
-- internal/theme, not here.

-- name: GetOrgTheme :one
SELECT * FROM org_themes WHERE org_id = sqlc.arg(org_id);

-- UpsertOrgTheme writes the whole row: the tenant screen always submits a
-- complete choice (a preset, or a full token set, or both), so a partial
-- merge here could only produce a theme nobody asked for. `tokens` is stored
-- as `{}` when the org is on a plain preset — the resolver reads an empty
-- object as "no override".
-- name: UpsertOrgTheme :one
INSERT INTO org_themes (org_id, preset_id, tokens, font_id)
VALUES (sqlc.arg(org_id), sqlc.narg(preset_id), sqlc.arg(tokens), sqlc.narg(font_id))
ON CONFLICT (org_id) DO UPDATE
SET preset_id = EXCLUDED.preset_id,
    tokens    = EXCLUDED.tokens,
    font_id   = EXCLUDED.font_id
RETURNING *;
