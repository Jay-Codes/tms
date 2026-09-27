-- Phase 26 — a backfill is one decision, recorded as one row and undone as one.
--
-- `backfill_batches` carries the decision; `payments.backfill_batch_id` names
-- the money a `paid` batch wrote and `payment_schedules.backfill_batch_id` the
-- rows a `waived` batch closed. The undo walks both.

-- name: CreateBackfillBatch :one
INSERT INTO backfill_batches (org_id, contract_id, mode, until, import_batch_id, created_by_user_id, from_date)
VALUES (
    sqlc.arg(org_id), sqlc.arg(contract_id), sqlc.arg(mode), sqlc.arg(until),
    sqlc.narg(import_batch_id), sqlc.narg(created_by_user_id), sqlc.narg(from_date)
)
RETURNING *;

-- FinishBackfillBatch writes the counts once the rows are settled, so the list
-- shows what the call did even after an undo has taken the money back.
-- name: FinishBackfillBatch :one
UPDATE backfill_batches
SET periods = sqlc.arg(periods), amount = sqlc.arg(amount),
    created_periods = sqlc.arg(created_periods)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
RETURNING *;

-- DeleteEmptyBackfillBatch drops the row a call that settled nothing opened:
-- a landlord checking is not a decision, and an empty line with an Undo
-- button would be noise.
-- name: DeleteEmptyBackfillBatch :exec
DELETE FROM backfill_batches
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND periods = 0 AND created_periods = 0;

-- name: StampPaymentBackfillBatch :exec
UPDATE payments SET backfill_batch_id = sqlc.arg(backfill_batch_id)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id);

-- WaiveBackfillSchedule is WaiveSchedule with the batch stamped on the row,
-- because a waiver has no payment to carry it.
-- name: WaiveBackfillSchedule :one
UPDATE payment_schedules
SET status = 'waived', backfill_batch_id = sqlc.arg(backfill_batch_id)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
  AND status IN ('pending', 'partial', 'overdue') AND deleted_at IS NULL
RETURNING *;

-- ListBackfillBatchesForContract is the contract page's "Backfills" list,
-- newest first. `touched` is the undo's own refusal, asked in advance so the
-- button can say so before it is pressed.
-- name: ListBackfillBatchesForContract :many
SELECT b.*,
       cu.full_name AS created_by_name,
       uu.full_name AS undone_by_name,
       EXISTS (
           SELECT 1
           FROM payment_allocations a2
           JOIN payments pm2 ON pm2.id = a2.payment_id
           WHERE a2.org_id = b.org_id
             AND pm2.status = 'recorded' AND pm2.deleted_at IS NULL
             AND pm2.backfill_batch_id IS DISTINCT FROM b.id
             AND pm2.created_at > b.created_at
             AND a2.schedule_id IN (
                 SELECT a.schedule_id FROM payment_allocations a
                 JOIN payments pm ON pm.id = a.payment_id
                 WHERE pm.backfill_batch_id = b.id AND pm.org_id = b.org_id
                 UNION
                 SELECT s.id FROM payment_schedules s
                 WHERE s.backfill_batch_id = b.id AND s.org_id = b.org_id
                 UNION
                 SELECT s.id FROM payment_schedules s
                 WHERE s.created_by_backfill_id = b.id AND s.org_id = b.org_id
             )
       )::boolean AS touched
FROM backfill_batches b
LEFT JOIN users cu ON cu.id = b.created_by_user_id
LEFT JOIN users uu ON uu.id = b.undone_by_user_id
WHERE b.org_id = sqlc.arg(org_id) AND b.contract_id = sqlc.arg(contract_id)
ORDER BY b.created_at DESC, b.id DESC;

-- name: GetBackfillBatch :one
SELECT b.*,
       cu.full_name AS created_by_name,
       uu.full_name AS undone_by_name
FROM backfill_batches b
LEFT JOIN users cu ON cu.id = b.created_by_user_id
LEFT JOIN users uu ON uu.id = b.undone_by_user_id
WHERE b.org_id = sqlc.arg(org_id) AND b.id = sqlc.arg(id);

-- LockBackfillBatch is the undo's guard: two clicks on Undo find the stamp the
-- first one wrote.
-- name: LockBackfillBatch :one
SELECT * FROM backfill_batches
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
FOR UPDATE;

-- BackfillBatchTouched is the refusal PLAN2 names: a live payment that is not
-- the batch's own, recorded after the batch, allocated to any period the batch
-- settled or waived. Undoing then would pull the floor from under money the
-- landlord has since recorded against the same months.
-- name: BackfillBatchTouched :one
SELECT EXISTS (
    SELECT 1
    FROM payment_allocations a2
    JOIN payments pm2 ON pm2.id = a2.payment_id
    WHERE a2.org_id = sqlc.arg(org_id)
      AND pm2.status = 'recorded' AND pm2.deleted_at IS NULL
      AND pm2.backfill_batch_id IS DISTINCT FROM sqlc.arg(id)::uuid
      AND pm2.created_at > sqlc.arg(created_at)::timestamptz
      AND a2.schedule_id IN (
          SELECT a.schedule_id FROM payment_allocations a
          JOIN payments pm ON pm.id = a.payment_id
          WHERE pm.backfill_batch_id = sqlc.arg(id)::uuid AND pm.org_id = sqlc.arg(org_id)
          UNION
          SELECT s.id FROM payment_schedules s
          WHERE s.backfill_batch_id = sqlc.arg(id)::uuid AND s.org_id = sqlc.arg(org_id)
          UNION
          SELECT s.id FROM payment_schedules s
          WHERE s.created_by_backfill_id = sqlc.arg(id)::uuid AND s.org_id = sqlc.arg(org_id)
      )
)::boolean AS touched;

-- ListBackfillBatchPayments is the undo's worklist of money: every live
-- payment the batch wrote, oldest first.
-- name: ListBackfillBatchPayments :many
SELECT id FROM payments
WHERE org_id = sqlc.arg(org_id) AND backfill_batch_id = sqlc.arg(backfill_batch_id)
  AND status = 'recorded' AND deleted_at IS NULL
ORDER BY paid_at, id;

-- UnwaiveBackfillSchedules reopens the rows a `waived` batch closed, with the
-- status recomputed from scratch exactly as UndoScheduleAdjustment does.
-- name: UnwaiveBackfillSchedules :many
UPDATE payment_schedules
SET status = CASE
        WHEN paid_amount >= amount THEN 'paid'
        WHEN due_date + sqlc.arg(grace_days)::int < CURRENT_DATE THEN 'overdue'
        WHEN paid_amount > 0 THEN 'partial'
        ELSE 'pending'
    END
WHERE org_id = sqlc.arg(org_id) AND backfill_batch_id = sqlc.arg(backfill_batch_id)
  AND status = 'waived' AND deleted_at IS NULL
RETURNING *;

-- name: MarkBackfillBatchUndone :one
UPDATE backfill_batches
SET undone_at = now(), undone_by_user_id = sqlc.narg(undone_by_user_id),
    undo_reason = sqlc.arg(undo_reason)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND undone_at IS NULL
RETURNING *;

-- ListBackfillBatchesForImport is what an import undo takes back: the live
-- batches its lines wrote.
-- name: ListBackfillBatchesForImport :many
SELECT * FROM backfill_batches
WHERE org_id = sqlc.arg(org_id) AND import_batch_id = sqlc.arg(import_batch_id)
  AND undone_at IS NULL
ORDER BY created_at, id;

-- FindUnitByCodeInOrg resolves the `unit_code` column of a backfill sheet — the
-- code printed on the unit's QR sticker, unique platform-wide, but only ever
-- matched inside the caller's org.
-- name: FindUnitByCodeInOrg :one
SELECT u.id, u.name, u.unit_code, p.name AS property_name
FROM units u
JOIN properties p ON p.id = u.property_id AND p.org_id = u.org_id
WHERE u.org_id = sqlc.arg(org_id) AND u.deleted_at IS NULL AND p.deleted_at IS NULL
  AND upper(u.unit_code) = upper(btrim(sqlc.arg(unit_code)::text));

-- FindRunningContractForUnitAndRenter is the tenancy a backfill line settles:
-- only a running contract takes a backfill (API.md §20.3).
-- name: FindRunningContractForUnitAndRenter :many
SELECT id FROM contracts
WHERE org_id = sqlc.arg(org_id) AND unit_id = sqlc.arg(unit_id)
  AND renter_user_id = sqlc.arg(renter_user_id) AND deleted_at IS NULL
  AND status IN ('active', 'expiring')
ORDER BY start_date DESC, created_at DESC
LIMIT 1;
