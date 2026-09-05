-- Phase 2: properties, units, pricing, payment periods.
--
-- Only what the Phase 2 contract needs on top of the 000002 schema:
--   * units.status_override         — distinguishes a landlord-set status
--     (unlisted / maintenance) from one derived from contract state, so a
--     later contract event knows whether it may move the unit back to vacant.
--   * price_plans.created_by_user_id — the price history endpoint shows
--     `created_by_name` (API.md GET /units/{id}/prices).
--   * uniqueness for a payment period's label and day-count among the org's
--     ACTIVE periods, so the 409 in POST /org/payment-periods is enforced by
--     the database and not only by a check-then-insert race.

ALTER TABLE units ADD COLUMN status_override BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE price_plans ADD COLUMN created_by_user_id UUID REFERENCES users (id) ON DELETE SET NULL;

CREATE UNIQUE INDEX payment_periods_org_days_key
    ON payment_periods (org_id, days)
    WHERE active AND deleted_at IS NULL;

CREATE UNIQUE INDEX payment_periods_org_label_key
    ON payment_periods (org_id, lower(label))
    WHERE active AND deleted_at IS NULL;

-- The vacancy board filters by status inside an org (GET /units?status=).
CREATE INDEX units_org_status_idx ON units (org_id, status) WHERE deleted_at IS NULL;
