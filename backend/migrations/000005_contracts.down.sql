ALTER TABLE notification_log DROP CONSTRAINT IF EXISTS notification_log_kind_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_kind_check
    CHECK (kind IN ('reminder_7d', 'reminder_due', 'overdue_daily', 'thank_you',
                    'otp', 'custom', 'link_approved', 'link_rejected'));

-- The seeded default templates are the only rows this migration created; they
-- go with it. Templates an org edited or added itself are left alone.
DELETE FROM contract_templates
WHERE name = 'Standard tenancy agreement' AND is_default;

DROP INDEX IF EXISTS contract_templates_default_key;

ALTER TABLE payment_schedules DROP COLUMN IF EXISTS paid_amount;

ALTER TABLE contracts DROP CONSTRAINT IF EXISTS contracts_due_day_check;
UPDATE contracts SET due_day = NULL WHERE due_day > 28;
ALTER TABLE contracts ADD CONSTRAINT contracts_due_day_check
    CHECK (due_day IS NULL OR (due_day BETWEEN 1 AND 28));

DROP INDEX IF EXISTS contracts_unit_live_key;
DROP INDEX IF EXISTS contracts_org_status_idx;
DROP INDEX IF EXISTS contracts_lifecycle_idx;
DROP INDEX IF EXISTS contracts_link_request_idx;

ALTER TABLE contracts
    DROP COLUMN IF EXISTS link_request_id,
    DROP COLUMN IF EXISTS termination_effective_date,
    DROP COLUMN IF EXISTS termination_reason,
    DROP COLUMN IF EXISTS terminated_at,
    DROP COLUMN IF EXISTS activated_at;
