-- Projections: the landlord's own estimate of running costs a month
-- (27–30 Sep 2026 client feedback). NULL means "the last twelve months'
-- average", as before.
ALTER TABLE projection_scenarios
    ADD COLUMN monthly_expenses BIGINT CHECK (monthly_expenses IS NULL OR monthly_expenses >= 0);
