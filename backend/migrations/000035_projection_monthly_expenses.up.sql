-- Projections (27–30 Sep 2026 client feedback):
--   * `monthly_expenses`: the landlord's own estimate of running costs a
--     month. NULL means "the last twelve months' average", as before.
--   * `from_purchase`: count only money from each property's purchase date
--     on. Off (the default), every expense ever logged counts.
--   * `include_future_expenses`: add the coming year's projected expenses to
--     the total ROI is measured against.
ALTER TABLE projection_scenarios
    ADD COLUMN monthly_expenses        BIGINT CHECK (monthly_expenses IS NULL OR monthly_expenses >= 0),
    ADD COLUMN from_purchase           BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN include_future_expenses BOOLEAN NOT NULL DEFAULT false;
