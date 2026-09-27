-- Phase 28 — projections, break-even and ROI (PLAN2 Phase 28).
--
--   * `properties.purchase_price`, `purchase_date`, `current_value`: the
--     landlord's investment in a property. All optional: without a purchase
--     price the projection still forecasts income, expenses and net, and only
--     ROI / break-even / payback are left blank.
--   * `expense_categories.is_capital`: spend filed under a capital category
--     (a renovation, a new roof) is investment, added to the purchase price,
--     not a running cost that drags down the monthly net.
--   * `projection_scenarios`: a landlord's named "what if" sets of the
--     projection's scenario parameters, per org.

ALTER TABLE properties
    ADD COLUMN purchase_price BIGINT CHECK (purchase_price IS NULL OR purchase_price > 0),
    ADD COLUMN purchase_date  DATE,
    ADD COLUMN current_value  BIGINT CHECK (current_value IS NULL OR current_value > 0);

ALTER TABLE expense_categories
    ADD COLUMN is_capital BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE projection_scenarios (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id              UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name                TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
    horizon_months      INT NOT NULL CHECK (horizon_months BETWEEN 1 AND 120),
    rent_change_pct     DOUBLE PRECISION NOT NULL DEFAULT 0,
    occupancy_pct       DOUBLE PRECISION,
    collection_rate_pct DOUBLE PRECISION,
    expense_change_pct  DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_by_user_id  UUID REFERENCES users (id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX projection_scenarios_name ON projection_scenarios (org_id, lower(name));
