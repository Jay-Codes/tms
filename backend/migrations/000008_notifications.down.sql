DROP INDEX IF EXISTS notification_log_sending_idx;
DROP INDEX IF EXISTS notification_log_org_created_idx;
DROP INDEX IF EXISTS notification_log_batch_idx;

ALTER TABLE notification_log DROP COLUMN IF EXISTS batch_id;

-- Rows mid-flight would violate the narrower constraint; put them back on the
-- queue rather than blocking the rollback.
UPDATE notification_log SET status = 'queued' WHERE status = 'sending';
ALTER TABLE notification_log DROP CONSTRAINT IF EXISTS notification_log_status_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_status_check
    CHECK (status IN ('queued', 'sent', 'failed'));

DELETE FROM notification_log WHERE kind = 'unsigned_reminder';
ALTER TABLE notification_log DROP CONSTRAINT IF EXISTS notification_log_kind_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_kind_check
    CHECK (kind IN ('reminder_7d', 'reminder_due', 'overdue_daily', 'thank_you',
                    'otp', 'custom', 'link_approved', 'link_rejected',
                    'contract_ready', 'welcome', 'contract_terminated'));
