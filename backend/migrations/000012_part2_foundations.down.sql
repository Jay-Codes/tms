-- Reverse of 000012, in the opposite order.
--
-- One thing is deliberately not reversed: the data fix that left each org with
-- a single recommended payment period. Restoring the badge to all four presets
-- would put the meaningless "Recommended on everything" state back in front of
-- renters, and the migration cannot know which rows it cleared. Rolling back
-- only removes the constraint that keeps the fix true.

-- Held messages have nowhere to sit under the old constraint; a top-up would
-- have released them to `queued` anyway, so that is where they go.
UPDATE notification_log SET status = 'queued' WHERE status = 'held_no_credit';
ALTER TABLE notification_log DROP CONSTRAINT notification_log_status_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_status_check
    CHECK (status IN ('queued', 'sending', 'sent', 'failed'));

DROP TRIGGER IF EXISTS sms_credit_ledger_no_delete ON sms_credit_ledger;
DROP TRIGGER IF EXISTS sms_credit_ledger_no_update ON sms_credit_ledger;
DROP TABLE IF EXISTS sms_credit_ledger;
DROP FUNCTION IF EXISTS sms_credit_ledger_append_only();
DROP TABLE IF EXISTS org_sms_credits;

DROP TABLE IF EXISTS platform_template_versions;
DROP TABLE IF EXISTS platform_templates;

DROP TABLE IF EXISTS org_themes;

DROP TABLE IF EXISTS expenses;
DROP TABLE IF EXISTS expense_categories;

DROP INDEX IF EXISTS payment_periods_one_recommended_per_org;

ALTER TABLE users DROP COLUMN IF EXISTS locale;

-- The template re-wording is not reversed either: `{{rent_basis}}` renders to
-- the empty string under the old code rather than breaking, and restoring the
-- old sentence would put the wrong rent figure back in new documents.
