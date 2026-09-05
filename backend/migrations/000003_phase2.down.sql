DROP INDEX IF EXISTS units_org_status_idx;
DROP INDEX IF EXISTS payment_periods_org_label_key;
DROP INDEX IF EXISTS payment_periods_org_days_key;
ALTER TABLE price_plans DROP COLUMN IF EXISTS created_by_user_id;
ALTER TABLE units DROP COLUMN IF EXISTS status_override;
