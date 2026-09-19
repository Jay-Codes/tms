-- Reverse Phase 16.1.
--
-- The seeded wording goes only if no admin has edited it (version = 1), the
-- rule 000016's down migration set: a rollback must not throw away wording
-- somebody wrote.
DELETE FROM platform_template_versions
WHERE kind = 'proof_rejected'
  AND kind IN (SELECT kind FROM platform_templates WHERE version = 1);
DELETE FROM platform_templates WHERE kind = 'proof_rejected' AND version = 1;

DROP TABLE IF EXISTS payment_proofs;

-- The kind list returns to what 000008 left it as. Any `proof_rejected` row
-- already in the log would refuse the constraint, so it goes with the table it
-- belonged to.
DELETE FROM notification_log WHERE kind = 'proof_rejected';
ALTER TABLE notification_log DROP CONSTRAINT IF EXISTS notification_log_kind_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_kind_check
    CHECK (kind IN ('reminder_7d', 'reminder_due', 'overdue_daily', 'thank_you',
                    'otp', 'custom', 'link_approved', 'link_rejected',
                    'contract_ready', 'welcome', 'contract_terminated',
                    'unsigned_reminder'));
