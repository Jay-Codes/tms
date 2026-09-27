DELETE FROM platform_templates WHERE kind IN ('payment_reversed', 'payment_corrected');
DELETE FROM notification_log WHERE kind IN ('payment_reversed', 'payment_corrected');
ALTER TABLE notification_log DROP CONSTRAINT notification_log_kind_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_kind_check
    CHECK (kind IN ('reminder_7d', 'reminder_due', 'overdue_daily', 'thank_you',
                    'otp', 'custom', 'link_approved', 'link_rejected',
                    'contract_ready', 'welcome', 'contract_terminated',
                    'unsigned_reminder', 'proof_rejected',
                    'name_corrected', 'backfill_done',
                    'notice_received', 'eviction_demand', 'eviction_notice', 'eviction_withdrawn'));
DROP TABLE IF EXISTS org_inbox_reads;
DROP TABLE IF EXISTS org_inbox;
DROP INDEX IF EXISTS payments_idempotency_key;
ALTER TABLE payments DROP COLUMN IF EXISTS corrects_payment_id, DROP COLUMN IF EXISTS idempotency_key;
