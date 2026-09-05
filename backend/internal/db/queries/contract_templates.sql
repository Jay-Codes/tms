-- Contract templates are the org's terms library. Editing one never touches an
-- existing contract: a contract stores its own rendered `terms_snapshot_html`
-- (SPEC §4, "contracts snapshot terms and rent at signing").

-- `body_html` is the English body and `body_html_sw` the Swahili one
-- (Phase 13). An empty Swahili body means "this org has no Swahili terms":
-- a Swahili contract then renders from the English body rather than a blank.
-- name: CreateContractTemplate :one
INSERT INTO contract_templates (org_id, name, body_html, body_html_sw, is_default)
VALUES (sqlc.arg(org_id), sqlc.arg(name), sqlc.arg(body_html),
        COALESCE(sqlc.narg(body_html_sw)::text, ''), sqlc.arg(is_default))
RETURNING *;

-- name: ListContractTemplates :many
SELECT * FROM contract_templates
WHERE org_id = sqlc.arg(org_id) AND deleted_at IS NULL
ORDER BY is_default DESC, created_at DESC, id DESC;

-- name: GetContractTemplate :one
SELECT * FROM contract_templates
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL;

-- name: GetDefaultContractTemplate :one
SELECT * FROM contract_templates
WHERE org_id = sqlc.arg(org_id) AND is_default AND deleted_at IS NULL
LIMIT 1;

-- name: UpdateContractTemplate :one
UPDATE contract_templates
SET name         = COALESCE(sqlc.narg(name), name),
    body_html    = COALESCE(sqlc.narg(body_html), body_html),
    body_html_sw = COALESCE(sqlc.narg(body_html_sw), body_html_sw),
    is_default   = COALESCE(sqlc.narg(is_default), is_default)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- ClearDefaultTemplate runs before promoting another template, so the partial
-- unique index on (org_id) WHERE is_default never sees two.
-- name: ClearDefaultTemplate :exec
UPDATE contract_templates SET is_default = false
WHERE org_id = sqlc.arg(org_id) AND is_default AND deleted_at IS NULL
  AND id <> sqlc.arg(keep_id);

-- name: SoftDeleteContractTemplate :one
UPDATE contract_templates SET deleted_at = now()
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- name: CountContractsForTemplate :one
SELECT count(*) FROM contracts
WHERE org_id = sqlc.arg(org_id) AND template_id = sqlc.arg(template_id) AND deleted_at IS NULL;
