-- Phase 22 §22.1 — template assignment.

-- ResolveUnitTemplate answers "which template would a tenancy of this unit be
-- written on", and why: the unit's own, else its property's, else the org
-- default. A soft-deleted assignment is skipped, never an error.
-- name: ResolveUnitTemplate :one
SELECT t.id, t.name,
       (CASE WHEN t.id = u.contract_template_id THEN 'unit'
             WHEN t.id = p.contract_template_id THEN 'property'
             ELSE 'default' END)::text AS source
FROM units u
JOIN properties p ON p.id = u.property_id AND p.org_id = u.org_id
JOIN contract_templates t ON t.org_id = u.org_id AND t.deleted_at IS NULL
 AND t.id = COALESCE(
       (SELECT ut.id FROM contract_templates ut
        WHERE ut.id = u.contract_template_id AND ut.org_id = u.org_id AND ut.deleted_at IS NULL),
       (SELECT pt.id FROM contract_templates pt
        WHERE pt.id = p.contract_template_id AND pt.org_id = u.org_id AND pt.deleted_at IS NULL),
       (SELECT dt.id FROM contract_templates dt
        WHERE dt.org_id = u.org_id AND dt.is_default AND dt.deleted_at IS NULL))
WHERE u.org_id = sqlc.arg(org_id) AND u.id = sqlc.arg(unit_id) AND u.deleted_at IS NULL;

-- SetUnitsTemplate assigns (or, with NULL, clears) the template of a set of
-- the org's units in one statement. Ids from another org match nothing.
-- name: SetUnitsTemplate :many
UPDATE units SET contract_template_id = sqlc.narg(template_id)
WHERE org_id = sqlc.arg(org_id) AND id = ANY(sqlc.arg(unit_ids)::uuid[]) AND deleted_at IS NULL
RETURNING id;

-- SetPropertyTemplate assigns (or clears) a property's template.
-- name: SetPropertyTemplate :one
UPDATE properties SET contract_template_id = sqlc.narg(template_id)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING id, contract_template_id;

-- TemplateUsage counts where each of the org's live templates is assigned, for
-- the templates list and for the delete guard.
-- name: TemplateUsage :many
SELECT t.id,
       (SELECT count(*) FROM units u
        WHERE u.org_id = t.org_id AND u.contract_template_id = t.id AND u.deleted_at IS NULL)::bigint AS units,
       (SELECT count(*) FROM properties p
        WHERE p.org_id = t.org_id AND p.contract_template_id = t.id AND p.deleted_at IS NULL)::bigint AS properties
FROM contract_templates t
WHERE t.org_id = sqlc.arg(org_id) AND t.deleted_at IS NULL;

-- ---------------------------------------------- §22.2 contract policies --

-- SetTemplatePolicy writes (or, with NULL, clears) a template's policy.
-- name: SetTemplatePolicy :one
UPDATE contract_templates SET policy = sqlc.narg(policy)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- SetContractPolicy stores the policy copy on a contract written in the same
-- transaction; it only ever touches a contract that has none yet.
-- name: SetContractPolicy :exec
UPDATE contracts SET policy = sqlc.arg(policy)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND policy IS NULL;
