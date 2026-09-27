DELETE FROM platform_templates WHERE kind IN ('notice_received', 'eviction_demand', 'eviction_notice', 'eviction_withdrawn');
DELETE FROM notification_log WHERE kind IN ('notice_received', 'eviction_demand', 'eviction_notice', 'eviction_withdrawn');
ALTER TABLE notification_log DROP CONSTRAINT notification_log_kind_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_kind_check
    CHECK (kind IN ('reminder_7d', 'reminder_due', 'overdue_daily', 'thank_you',
                    'otp', 'custom', 'link_approved', 'link_rejected',
                    'contract_ready', 'welcome', 'contract_terminated',
                    'unsigned_reminder', 'proof_rejected',
                    'name_corrected', 'backfill_done'));
DROP TABLE IF EXISTS eviction_cases;
ALTER TABLE contracts
    DROP COLUMN IF EXISTS moved_out_confirmed_at,
    DROP COLUMN IF EXISTS notice_reason,
    DROP COLUMN IF EXISTS notice_leave_on,
    DROP COLUMN IF EXISTS notice_given_at;
UPDATE payment_schedules SET amount = original_amount WHERE original_amount IS NOT NULL;
ALTER TABLE payment_schedules
    DROP COLUMN IF EXISTS adjusted_by_user_id,
    DROP COLUMN IF EXISTS adjusted_at,
    DROP COLUMN IF EXISTS adjustment_reason,
    DROP COLUMN IF EXISTS adjustment_kind,
    DROP COLUMN IF EXISTS original_amount;
