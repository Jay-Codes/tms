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

-- ------------------------------------------------ §22.5 rent refunds in reports --
-- Cash reports subtract rent refunded in the period it was paid out. Each
-- mirrors one collected-money query on the same axis; the handler subtracts.

-- name: RefundedPeriod :one
SELECT COALESCE(sum(amount), 0)::bigint AS refunded
FROM rent_refunds
WHERE org_id = sqlc.arg(org_id)
  AND refunded_at >= sqlc.arg(from_ts) AND refunded_at < sqlc.arg(to_ts);

-- name: RefundedBuckets :many
SELECT date_trunc(sqlc.arg(bucket)::text, rf.refunded_at AT TIME ZONE 'Africa/Dar_es_Salaam')::date AS bucket_start,
       COALESCE(sum(rf.amount), 0)::bigint AS refunded
FROM rent_refunds rf
WHERE rf.org_id = sqlc.arg(org_id)
  AND rf.refunded_at >= sqlc.arg(from_ts) AND rf.refunded_at < sqlc.arg(to_ts)
GROUP BY 1
ORDER BY 1;

-- name: RefundedDaily :many
SELECT (rf.refunded_at AT TIME ZONE 'Africa/Dar_es_Salaam')::date AS day,
       COALESCE(sum(rf.amount), 0)::bigint AS amount
FROM rent_refunds rf
WHERE rf.org_id = sqlc.arg(org_id)
  AND rf.refunded_at >= sqlc.arg(from_ts) AND rf.refunded_at < sqlc.arg(to_ts)
  AND (sqlc.narg(property_id)::uuid IS NULL OR EXISTS (
        SELECT 1 FROM contracts c
        JOIN units u ON u.id = c.unit_id AND u.org_id = c.org_id
        WHERE c.id = rf.contract_id AND c.org_id = rf.org_id
          AND u.property_id = sqlc.narg(property_id)::uuid))
GROUP BY 1
ORDER BY 1;

-- name: RefundedByProperty :many
SELECT u.property_id AS property_id,
       COALESCE(sum(rf.amount), 0)::bigint AS amount
FROM rent_refunds rf
JOIN contracts c ON c.id = rf.contract_id AND c.org_id = rf.org_id
JOIN units u     ON u.id = c.unit_id AND u.org_id = c.org_id
WHERE rf.org_id = sqlc.arg(org_id)
  AND rf.refunded_at >= sqlc.arg(from_ts) AND rf.refunded_at < sqlc.arg(to_ts)
  AND (sqlc.narg(property_id)::uuid IS NULL OR u.property_id = sqlc.narg(property_id)::uuid)
GROUP BY 1;

-- ------------------------------------------------ §22.5 settle-up, deposits --

-- SetScheduleAmount re-prices one period (pro-rata move-out) and recomputes
-- its status from its own money and dates.
-- name: SetScheduleAmount :one
UPDATE payment_schedules
SET amount = sqlc.arg(amount),
    status = CASE
        WHEN status IN ('waived', 'written_off') THEN status
        WHEN paid_amount >= sqlc.arg(amount)::bigint THEN 'paid'
        WHEN due_date + sqlc.arg(grace_days)::int < CURRENT_DATE THEN 'overdue'
        WHEN paid_amount > 0 THEN 'partial'
        ELSE 'pending'
    END
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- SetSchedulePaid lowers a period's paid amount after money was refunded off it.
-- name: SetSchedulePaid :exec
UPDATE payment_schedules SET paid_amount = sqlc.arg(paid_amount)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL;

-- ListLiveAllocationsForSchedule: newest first, so a refund takes back the
-- most recent money first.
-- name: ListLiveAllocationsForSchedule :many
SELECT a.id, a.payment_id, a.amount
FROM payment_allocations a
JOIN payments p ON p.id = a.payment_id AND p.org_id = a.org_id
WHERE a.org_id = sqlc.arg(org_id) AND a.schedule_id = sqlc.arg(schedule_id)
  AND p.status <> 'reversed' AND p.deleted_at IS NULL
ORDER BY a.created_at DESC, a.id DESC;

-- name: CreateRentRefund :one
INSERT INTO rent_refunds (org_id, contract_id, amount, method, reference, reason, refunded_at, recorded_by_user_id)
VALUES (sqlc.arg(org_id), sqlc.arg(contract_id), sqlc.arg(amount), sqlc.arg(method),
        sqlc.narg(reference), sqlc.arg(reason), sqlc.arg(refunded_at), sqlc.arg(recorded_by_user_id))
RETURNING *;

-- name: AddRentRefundItem :exec
INSERT INTO rent_refund_items (refund_id, org_id, payment_id, amount)
VALUES (sqlc.arg(refund_id), sqlc.arg(org_id), sqlc.arg(payment_id), sqlc.arg(amount))
ON CONFLICT (refund_id, payment_id) DO UPDATE SET amount = rent_refund_items.amount + EXCLUDED.amount;

-- PaymentRefunded: a payment part of which was refunded cannot be reversed.
-- name: PaymentRefunded :one
SELECT EXISTS (SELECT 1 FROM rent_refund_items
               WHERE org_id = sqlc.arg(org_id) AND payment_id = sqlc.arg(payment_id)) AS refunded;

-- name: ListRentRefunds :many
SELECT * FROM rent_refunds
WHERE org_id = sqlc.arg(org_id) AND contract_id = sqlc.arg(contract_id)
ORDER BY refunded_at DESC;

-- name: SetContractSettlement :exec
UPDATE contracts SET settlement = sqlc.arg(settlement)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id);

-- name: ListDepositEntries :many
SELECT * FROM deposit_entries
WHERE org_id = sqlc.arg(org_id) AND contract_id = sqlc.arg(contract_id)
ORDER BY occurred_at, created_at;

-- DepositTotals is the deposit ledger summed by kind.
-- name: DepositTotals :one
SELECT COALESCE(sum(amount) FILTER (WHERE kind = 'received'), 0)::bigint        AS received,
       COALESCE(sum(amount) FILTER (WHERE kind = 'deduction'), 0)::bigint       AS deducted,
       COALESCE(sum(amount) FILTER (WHERE kind = 'refund'), 0)::bigint          AS refunded,
       COALESCE(sum(amount) FILTER (WHERE kind = 'applied_to_rent'), 0)::bigint AS applied
FROM deposit_entries
WHERE org_id = sqlc.arg(org_id) AND contract_id = sqlc.arg(contract_id);

-- name: CreateDepositEntry :one
INSERT INTO deposit_entries (org_id, contract_id, kind, amount, method, reference, reason,
                             payment_id, occurred_at, recorded_by_user_id)
VALUES (sqlc.arg(org_id), sqlc.arg(contract_id), sqlc.arg(kind), sqlc.arg(amount), sqlc.narg(method),
        sqlc.narg(reference), sqlc.narg(reason), sqlc.narg(payment_id), sqlc.arg(occurred_at),
        sqlc.arg(recorded_by_user_id))
RETURNING *;
