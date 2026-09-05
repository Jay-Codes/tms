DROP INDEX IF EXISTS notification_log_queued_idx;
ALTER TABLE notification_log DROP COLUMN IF EXISTS attempts;
ALTER TABLE notification_log DROP COLUMN IF EXISTS error;
ALTER TABLE notification_log DROP COLUMN IF EXISTS body;
ALTER TABLE notification_log DROP COLUMN IF EXISTS to_phone;

ALTER TABLE notification_log DROP CONSTRAINT IF EXISTS notification_log_kind_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_kind_check
    CHECK (kind IN ('reminder_7d', 'reminder_due', 'overdue_daily', 'thank_you', 'otp', 'custom'));

DROP INDEX IF EXISTS unit_link_requests_renter_created_idx;
DROP INDEX IF EXISTS unit_link_requests_org_created_idx;
DROP INDEX IF EXISTS unit_link_requests_pending_key;

DELETE FROM unit_link_requests WHERE status = 'cancelled';
ALTER TABLE unit_link_requests DROP CONSTRAINT IF EXISTS unit_link_requests_status_check;
ALTER TABLE unit_link_requests ADD CONSTRAINT unit_link_requests_status_check
    CHECK (status IN ('pending', 'approved', 'rejected'));

ALTER TABLE unit_link_requests ADD COLUMN reason TEXT;
ALTER TABLE unit_link_requests
    DROP COLUMN IF EXISTS accepted_terms_at,
    DROP COLUMN IF EXISTS decided_by_user_id,
    DROP COLUMN IF EXISTS decided_at,
    DROP COLUMN IF EXISTS rejection_reason,
    DROP COLUMN IF EXISTS end_date,
    DROP COLUMN IF EXISTS start_date,
    DROP COLUMN IF EXISTS term_days,
    DROP COLUMN IF EXISTS payment_period_id;
