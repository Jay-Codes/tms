DROP INDEX IF EXISTS audit_log_action_at_idx;
ALTER TABLE orgs DROP COLUMN IF EXISTS suspended_reason;
ALTER TABLE orgs DROP COLUMN IF EXISTS suspended_at;
