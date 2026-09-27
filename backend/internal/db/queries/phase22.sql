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

-- ------------------------------------------ §22.3 stale unsigned contracts --

-- TouchTemplateContent records that a template's wording or policy changed.
-- name: TouchTemplateContent :exec
UPDATE contract_templates SET content_updated_at = now()
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL;

-- ContractTemplateChanged: a contract nobody has signed yet, written before its
-- template's wording or policy last changed.
-- name: ContractTemplateChanged :one
SELECT EXISTS (
    SELECT 1 FROM contracts c
    JOIN contract_templates t ON t.id = c.template_id AND t.org_id = c.org_id
    WHERE c.org_id = sqlc.arg(org_id) AND c.id = sqlc.arg(id)
      AND c.status = 'pending_signature' AND c.deleted_at IS NULL
      AND c.created_at < t.content_updated_at
      AND NOT EXISTS (SELECT 1 FROM contract_signatures cs
                      WHERE cs.contract_id = c.id AND cs.org_id = c.org_id)
) AS changed;

-- ListStalePendingContracts is every unsigned contract on a template written
-- before the template's wording or policy last changed.
-- name: ListStalePendingContracts :many
SELECT c.id FROM contracts c
JOIN contract_templates t ON t.id = c.template_id AND t.org_id = c.org_id
WHERE c.org_id = sqlc.arg(org_id) AND t.id = sqlc.arg(template_id)
  AND c.status = 'pending_signature' AND c.deleted_at IS NULL
  AND c.created_at < t.content_updated_at
  AND NOT EXISTS (SELECT 1 FROM contract_signatures cs
                  WHERE cs.contract_id = c.id AND cs.org_id = c.org_id)
ORDER BY c.created_at;

-- SetContractSupersedes links a reissued contract to the one it replaced.
-- name: SetContractSupersedes :exec
UPDATE contracts SET supersedes_contract_id = sqlc.arg(supersedes_contract_id)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id);

-- ---------------------------------------------------- §22.4 amendments --

-- OpenAmendmentFor is the unsigned amendment of a contract, if any.
-- name: OpenAmendmentFor :one
SELECT id FROM contracts
WHERE org_id = sqlc.arg(org_id) AND supersedes_contract_id = sqlc.arg(contract_id)
  AND status = 'pending_signature' AND amendment_effective_date IS NOT NULL
  AND deleted_at IS NULL;

-- LockContract serialises an amendment's activation against the old contract.
-- name: LockContract :one
SELECT id, status FROM contracts
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
FOR UPDATE;

-- ListAllocationsFrom is every live allocation on a contract's periods starting
-- on or after a date — the money an amendment carries across.
-- name: ListAllocationsFrom :many
SELECT a.id, a.payment_id, a.schedule_id, a.amount
FROM payment_allocations a
JOIN payment_schedules s ON s.id = a.schedule_id AND s.org_id = a.org_id
JOIN payments p ON p.id = a.payment_id AND p.org_id = a.org_id
WHERE a.org_id = sqlc.arg(org_id) AND s.contract_id = sqlc.arg(contract_id)
  AND s.period_start >= sqlc.arg(from_date) AND s.deleted_at IS NULL
  AND p.status <> 'reversed' AND p.deleted_at IS NULL
ORDER BY s.period_start, a.created_at;

-- DeletePaymentAllocation removes an allocation whose money has been moved.
-- name: DeletePaymentAllocation :exec
DELETE FROM payment_allocations WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id);

-- WaiveScheduleFrom closes one of the old contract's periods the amendment
-- takes over, keeping only money that could not be moved.
-- name: WaiveScheduleFrom :exec
UPDATE payment_schedules SET status = 'waived', paid_amount = sqlc.arg(paid_amount)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL;

-- SupersedeContract closes the old side of an activated amendment: it points
-- at its successor and stops the day before the amendment governs. When that
-- day has already passed it ends now; otherwise the lifecycle job ends it.
-- name: SupersedeContract :one
UPDATE contracts
SET superseded_by_contract_id = sqlc.arg(new_id),
    termination_effective_date = sqlc.arg(last_day),
    termination_reason = sqlc.arg(reason),
    status = CASE WHEN sqlc.arg(last_day)::date < CURRENT_DATE THEN 'ended' ELSE status END
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND status IN ('active', 'expiring')
RETURNING id, status;

-- ShrinkPaymentAllocation keeps the part of an allocation that could not move.
-- name: ShrinkPaymentAllocation :exec
UPDATE payment_allocations SET amount = sqlc.arg(amount)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id);
