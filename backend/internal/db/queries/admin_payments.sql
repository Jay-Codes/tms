-- The overdue sweep (internal/payment.FlipOverdue) is a platform-wide job: a
-- due date is the same fact in every org. Like the other admin_* files it may
-- cross orgs and is exempt from the org_id repository guard — but it takes an
-- optional org_id so the org-scoped reads (GET /schedules, GET /me/schedules)
-- can run it for just their own tenant before answering (API.md Phase 5).
--
-- The grace period comes from each org's own settings, so the statement joins
-- orgs rather than taking one number for the whole platform. It is idempotent:
-- it selects only the statuses it is moving away from.

-- name: FlipOverdueSchedules :many
UPDATE payment_schedules s
SET status = 'overdue'
FROM orgs o
WHERE o.id = s.org_id
  AND s.status IN ('pending', 'partial')
  AND s.deleted_at IS NULL
  AND s.due_date + COALESCE((o.settings ->> 'grace_days')::int, 3) < CURRENT_DATE
  AND (sqlc.narg(org_id)::uuid IS NULL OR s.org_id = sqlc.narg(org_id)::uuid)
RETURNING s.id, s.org_id;
