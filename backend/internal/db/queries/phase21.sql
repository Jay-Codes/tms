-- Phase 21 §21.2 — arrears after a tenancy has closed.

-- ListFormerArrears is the "former tenants who still owe" board: closed
-- contracts (terminated or ended) with at least one row still owing, newest
-- closure first. `closed_on` is the day the tenancy stopped — the termination's
-- effective date, or the end date for one that ran out.
-- name: ListFormerArrears :many
SELECT c.id AS contract_id, c.status AS contract_status, c.renter_user_id,
       COALESCE(c.termination_effective_date, c.end_date)::date AS closed_on,
       ru.full_name AS renter_name, ru.phone AS renter_phone,
       u.id AS unit_id, u.name AS unit_name, p.id AS property_id, p.name AS property_name,
       owing.outstanding, owing.periods, owing.oldest_due,
       (SELECT max(pay.paid_at) FROM payments pay
        WHERE pay.org_id = c.org_id AND pay.contract_id = c.id
          AND pay.status <> 'reversed' AND pay.deleted_at IS NULL)::timestamptz AS last_paid_at
FROM contracts c
JOIN units u      ON u.id = c.unit_id AND u.org_id = c.org_id
JOIN properties p ON p.id = u.property_id AND p.org_id = c.org_id
JOIN users ru     ON ru.id = c.renter_user_id
JOIN LATERAL (
    SELECT COALESCE(sum(GREATEST(s.amount - s.paid_amount, 0)), 0)::bigint AS outstanding,
           count(*)::bigint AS periods,
           min(s.due_date)::date AS oldest_due
    FROM payment_schedules s
    WHERE s.org_id = c.org_id AND s.contract_id = c.id AND s.deleted_at IS NULL
      AND s.status IN ('pending', 'partial', 'overdue')
) owing ON owing.periods > 0
WHERE c.org_id = sqlc.arg(org_id) AND c.deleted_at IS NULL
  AND c.status IN ('ended', 'terminated')
  AND (sqlc.narg(property_id)::uuid IS NULL OR p.id = sqlc.narg(property_id)::uuid)
  AND (sqlc.narg(cursor_at)::date IS NULL
       OR (COALESCE(c.termination_effective_date, c.end_date), c.id)
          < (sqlc.narg(cursor_at)::date, sqlc.narg(cursor_id)::uuid))
ORDER BY COALESCE(c.termination_effective_date, c.end_date) DESC, c.id DESC
LIMIT sqlc.arg(row_limit);

-- FormerArrearsTotal is the headline over the same set, unpaged.
-- name: FormerArrearsTotal :one
SELECT COALESCE(sum(GREATEST(s.amount - s.paid_amount, 0)), 0)::bigint AS outstanding,
       count(DISTINCT c.id)::bigint AS contracts
FROM payment_schedules s
JOIN contracts c ON c.id = s.contract_id AND c.org_id = s.org_id
WHERE s.org_id = sqlc.arg(org_id) AND s.deleted_at IS NULL AND c.deleted_at IS NULL
  AND c.status IN ('ended', 'terminated')
  AND s.status IN ('pending', 'partial', 'overdue');

-- WriteOffSchedules closes every still-owing row of a contract as bad debt.
-- Money already received on a partial row stays where it is.
-- name: WriteOffSchedules :many
UPDATE payment_schedules
SET status = 'written_off', written_off_at = now(),
    written_off_by_user_id = sqlc.arg(actor_user_id), write_off_reason = sqlc.arg(reason)
WHERE org_id = sqlc.arg(org_id) AND contract_id = sqlc.arg(contract_id)
  AND status IN ('pending', 'partial', 'overdue') AND deleted_at IS NULL
RETURNING *;

-- RestoreWrittenOff undoes a write-off: each row's status is recomputed from
-- its own money and dates, the way a reversal recomputes it.
-- name: RestoreWrittenOff :many
UPDATE payment_schedules
SET status = CASE
        WHEN paid_amount >= amount THEN 'paid'
        WHEN due_date + sqlc.arg(grace_days)::int < CURRENT_DATE THEN 'overdue'
        WHEN paid_amount > 0 THEN 'partial'
        ELSE 'pending'
    END,
    written_off_at = NULL, written_off_by_user_id = NULL, write_off_reason = NULL
WHERE org_id = sqlc.arg(org_id) AND contract_id = sqlc.arg(contract_id)
  AND status = 'written_off' AND deleted_at IS NULL
RETURNING *;
