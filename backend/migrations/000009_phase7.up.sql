-- Phase 7: platform-admin suspension.
--
-- Suspension already existed as `orgs.status` (000002) and is already honoured
-- by the public endpoints and the notification scheduler. What was missing is
-- the paperwork around it: why an org was suspended and when, which the admin
-- org detail shows and the audit trail cross-references.
--
-- No `job_runs` table: GET /admin/jobs reports each job's last run from the
-- audit rows the jobs already write (`contract.lifecycle_run`,
-- `payment.overdue_run`, `notification.scheduler_run`), so there is one place
-- the truth lives rather than two.

ALTER TABLE orgs ADD COLUMN suspended_at     TIMESTAMPTZ;
ALTER TABLE orgs ADD COLUMN suspended_reason TEXT;

-- GET /admin/jobs reads the newest audit row per job action, platform-wide.
CREATE INDEX audit_log_action_at_idx ON audit_log (action, at DESC);
