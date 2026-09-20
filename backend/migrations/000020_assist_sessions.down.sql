-- Reverse Phase 18.

DROP TABLE IF EXISTS assist_sessions;

ALTER TABLE contract_signatures DROP COLUMN IF EXISTS witnessed_by_user_id;

-- The in-person rows go with the channel they belonged to: a `shown` row would
-- refuse both restored constraints.
ALTER TABLE notification_log DROP CONSTRAINT IF EXISTS notification_log_channel_status_check;
DELETE FROM notification_log WHERE channel = 'in_person' OR status = 'shown';

ALTER TABLE notification_log DROP CONSTRAINT IF EXISTS notification_log_status_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_status_check
    CHECK (status IN ('queued', 'sending', 'sent', 'failed', 'held_no_credit'));

ALTER TABLE notification_log DROP CONSTRAINT IF EXISTS notification_log_channel_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_channel_check
    CHECK (channel IN ('sms'));
