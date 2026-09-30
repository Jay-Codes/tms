-- Phase 22 §22.5 (rest) — single-period relief, notice to leave, holdover,
-- eviction stages.

-- ------------------------------------------------------ period relief --

-- AdjustSchedule waives one period (amount kept, status waived) or discounts
-- it (amount lowered, status recomputed); `original_amount` remembers what it
-- was so the adjustment can be undone.
-- name: AdjustSchedule :one
UPDATE payment_schedules
SET original_amount = amount,
    amount = CASE WHEN sqlc.arg(kind)::text = 'discount' THEN amount - sqlc.arg(discount)::bigint ELSE amount END,
    status = CASE
        WHEN sqlc.arg(kind)::text = 'waive' THEN 'waived'
        WHEN paid_amount >= amount - sqlc.arg(discount)::bigint THEN 'paid'
        WHEN due_date + sqlc.arg(grace_days)::int < CURRENT_DATE THEN 'overdue'
        WHEN paid_amount > 0 THEN 'partial'
        ELSE 'pending'
    END,
    adjustment_kind = sqlc.arg(kind), adjustment_reason = sqlc.arg(reason),
    adjusted_at = now(), adjusted_by_user_id = sqlc.arg(actor_user_id)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
  AND original_amount IS NULL AND status IN ('pending', 'partial', 'overdue')
RETURNING *;

-- name: UndoScheduleAdjustment :one
UPDATE payment_schedules
SET amount = original_amount,
    status = CASE
        WHEN paid_amount >= original_amount THEN 'paid'
        WHEN due_date + sqlc.arg(grace_days)::int < CURRENT_DATE THEN 'overdue'
        WHEN paid_amount > 0 THEN 'partial'
        ELSE 'pending'
    END,
    original_amount = NULL, adjustment_kind = NULL, adjustment_reason = NULL,
    adjusted_at = NULL, adjusted_by_user_id = NULL
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
  AND original_amount IS NOT NULL AND status IN ('pending', 'partial', 'overdue', 'paid', 'waived')
RETURNING *;

-- ---------------------------------------------------- notice to leave --

-- name: SetContractNotice :one
UPDATE contracts
SET notice_given_at = now(), notice_leave_on = sqlc.arg(leave_on), notice_reason = sqlc.narg(reason)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND status IN ('active', 'expiring')
RETURNING id;

-- name: ClearContractNotice :one
UPDATE contracts
SET notice_given_at = NULL, notice_leave_on = NULL, notice_reason = NULL
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND notice_leave_on IS NOT NULL
RETURNING id;

-- -------------------------------------------------------------- holdover --

-- ListHoldovers: tenancies that ran out (not terminated) in the last 90 days,
-- replaced by nothing, not yet confirmed empty — the tenant may still be there.
-- name: ListHoldovers :many
SELECT c.id AS contract_id, c.end_date, c.renter_user_id,
       ru.full_name AS renter_name, ru.phone AS renter_phone,
       u.id AS unit_id, u.name AS unit_name, p.name AS property_name
FROM contracts c
JOIN units u      ON u.id = c.unit_id AND u.org_id = c.org_id
JOIN properties p ON p.id = u.property_id AND p.org_id = c.org_id
JOIN users ru     ON ru.id = c.renter_user_id
WHERE c.org_id = sqlc.arg(org_id) AND c.deleted_at IS NULL
  AND c.status = 'ended' AND c.superseded_by_contract_id IS NULL AND NOT c.is_offline
  AND c.moved_out_confirmed_at IS NULL
  AND c.end_date >= CURRENT_DATE - 90
  AND NOT EXISTS (
      SELECT 1 FROM contracts n
      WHERE n.org_id = c.org_id AND n.supersedes_contract_id = c.id
        AND n.status IN ('pending_signature', 'active', 'expiring') AND n.deleted_at IS NULL)
ORDER BY c.end_date DESC, c.id DESC;

-- name: ConfirmMovedOut :one
UPDATE contracts SET moved_out_confirmed_at = now()
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
  AND status IN ('ended', 'terminated') AND moved_out_confirmed_at IS NULL
RETURNING id;

-- LinkRenewalOfEnded records a renewal of a tenancy that had already run out.
-- name: LinkRenewalOfEnded :exec
UPDATE contracts SET superseded_by_contract_id = sqlc.arg(new_id)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND status = 'ended';

-- -------------------------------------------------------------- eviction --

-- ArrearsDue is what a running contract owes on periods past their grace.
-- name: ArrearsDue :one
SELECT COALESCE(sum(GREATEST(amount - paid_amount, 0)), 0)::bigint AS owed
FROM payment_schedules
WHERE org_id = sqlc.arg(org_id) AND contract_id = sqlc.arg(contract_id) AND deleted_at IS NULL
  AND status IN ('pending', 'partial', 'overdue')
  AND due_date + sqlc.arg(grace_days)::int < CURRENT_DATE;

-- name: OpenEvictionCase :one
INSERT INTO eviction_cases (org_id, contract_id, stage, arrears_at_open, notice_days,
                            demand_issued_at, pay_by, opened_by_user_id)
VALUES (sqlc.arg(org_id), sqlc.arg(contract_id), 'demand', sqlc.arg(arrears), sqlc.arg(notice_days),
        now(), sqlc.arg(pay_by), sqlc.arg(actor_user_id))
RETURNING *;

-- name: GetEvictionCase :one
SELECT * FROM eviction_cases WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id);

-- name: ListEvictionCasesForContract :many
SELECT * FROM eviction_cases
WHERE org_id = sqlc.arg(org_id) AND contract_id = sqlc.arg(contract_id)
ORDER BY created_at DESC;

-- name: ListOpenEvictionCases :many
SELECT e.*, ru.full_name AS renter_name, u.name AS unit_name, p.name AS property_name
FROM eviction_cases e
JOIN contracts c  ON c.id = e.contract_id AND c.org_id = e.org_id
JOIN units u      ON u.id = c.unit_id AND u.org_id = c.org_id
JOIN properties p ON p.id = u.property_id AND p.org_id = c.org_id
JOIN users ru     ON ru.id = c.renter_user_id
WHERE e.org_id = sqlc.arg(org_id) AND e.stage IN ('demand', 'notice')
ORDER BY e.created_at DESC;

-- name: EvictionToNotice :one
UPDATE eviction_cases
SET stage = 'notice', notice_issued_at = now(), vacate_by = sqlc.arg(vacate_by), updated_at = now()
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND stage = 'demand'
RETURNING *;

-- name: CloseEvictionCase :one
UPDATE eviction_cases
SET stage = sqlc.arg(stage), closed_at = now(), close_reason = sqlc.narg(reason), updated_at = now()
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND stage IN ('demand', 'notice')
RETURNING *;

-- CloseEvictionOnTermination marks an open case vacated when its contract is
-- terminated.
-- name: CloseEvictionOnTermination :exec
UPDATE eviction_cases
SET stage = 'vacated', closed_at = now(), close_reason = 'tenancy terminated', updated_at = now()
WHERE org_id = sqlc.arg(org_id) AND contract_id = sqlc.arg(contract_id) AND stage IN ('demand', 'notice');
