-- Calendar-month billing (30 Sep 2026 client feedback).
--
-- A payment period counted in days drifts against the calendar: twelve
-- 30-day periods are 360 days, so "rent on the 1st" slides to the 27th within
-- a year. Many landlords bill on a fixed day of every month and their tenants
-- expect exactly that, so a period can now be counted in calendar months.
--
--   * payment_periods.months — NULL for a period counted in days (every
--     period until now). When set, the period steps by that many calendar
--     months, on the contract's due day (the 1st when none is set), and
--     `days` holds its nominal length (30 × months), which is still what rent
--     is scaled by and what reports average over.
--   * contracts.payment_period_months — the cadence snapshotted at creation,
--     next to payment_period_days, so a later edit of the period never
--     changes a contract already issued.
--
-- The day-count uniqueness index now tells the two kinds apart: "Monthly
-- (30 days)" and "Monthly (calendar)" share a nominal 30 days.
ALTER TABLE payment_periods
    ADD COLUMN months INT CHECK (months IS NULL OR months BETWEEN 1 AND 12);

ALTER TABLE contracts
    ADD COLUMN payment_period_months INT
        CHECK (payment_period_months IS NULL OR payment_period_months BETWEEN 1 AND 12);

DROP INDEX payment_periods_org_days_key;
CREATE UNIQUE INDEX payment_periods_org_days_key
    ON payment_periods (org_id, days, COALESCE(months, 0))
    WHERE active AND deleted_at IS NULL;
