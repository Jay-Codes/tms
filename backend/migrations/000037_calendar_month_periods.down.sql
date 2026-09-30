DROP INDEX payment_periods_org_days_key;
UPDATE payment_periods SET active = false WHERE months IS NOT NULL;
CREATE UNIQUE INDEX payment_periods_org_days_key
    ON payment_periods (org_id, days)
    WHERE active AND deleted_at IS NULL;

ALTER TABLE contracts DROP COLUMN payment_period_months;
ALTER TABLE payment_periods DROP COLUMN months;
