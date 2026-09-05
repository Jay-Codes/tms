-- The contract lifecycle job (internal/contract.RunLifecycle) is a platform-wide
-- sweep: it flags contracts approaching their end date and closes the ones past
-- it, across every org. Like the other admin_* files it crosses orgs by design
-- and is exempt from the org_id repository guard.
--
-- Both statements are idempotent — they select on the status they are moving
-- away from — so running the job twice in one hour changes nothing the second
-- time, and an on-demand run from POST /admin/jobs/contract-lifecycle is safe.

-- name: FlagExpiringContracts :many
UPDATE contracts
SET status = 'expiring'
WHERE status = 'active' AND deleted_at IS NULL
  AND end_date - sqlc.arg(notice_days)::int <= CURRENT_DATE
  AND end_date > CURRENT_DATE
RETURNING id, org_id, unit_id;

-- name: EndLapsedContracts :many
UPDATE contracts
SET status = 'ended'
WHERE status IN ('active', 'expiring') AND deleted_at IS NULL
  AND end_date <= CURRENT_DATE
RETURNING id, org_id, unit_id;

-- FreeUnitsWithoutLiveContract flips a unit back to vacant once nothing runs on
-- it, clearing the landlord's override with it: a tenancy that has ended must
-- put the unit back on the vacancy board (FLOWS 4.2).
-- name: FreeUnitsWithoutLiveContract :many
UPDATE units
SET status = 'vacant', status_override = false
WHERE id = ANY (sqlc.arg(unit_ids)::uuid[])
  AND status <> 'vacant' AND deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM contracts c
      WHERE c.unit_id = units.id AND c.deleted_at IS NULL
        AND c.status IN ('pending_signature', 'active', 'expiring')
  )
RETURNING id;
