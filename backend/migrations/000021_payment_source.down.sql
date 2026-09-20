-- Reverses 000021. The template rows go first: the notification-kind CHECK
-- cannot narrow while a log row names one of the new kinds, and dropping the
-- wording is the signal that nothing should raise them any more.
DELETE FROM platform_templates WHERE kind IN ('name_corrected', 'backfill_done');
DELETE FROM notification_log WHERE kind IN ('name_corrected', 'backfill_done');

ALTER TABLE notification_log DROP CONSTRAINT notification_log_kind_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_kind_check
    CHECK (kind IN ('reminder_7d', 'reminder_due', 'overdue_daily', 'thank_you',
                    'otp', 'custom', 'link_approved', 'link_rejected',
                    'contract_ready', 'welcome', 'contract_terminated',
                    'unsigned_reminder', 'proof_rejected'));

-- Dropping the column loses only the distinction between `manual` and
-- `backfill`; `import` round-trips, because `import_batch_id` is still what it
-- was derived from.
DROP INDEX IF EXISTS payments_org_source_idx;
ALTER TABLE payments DROP COLUMN IF EXISTS source;
