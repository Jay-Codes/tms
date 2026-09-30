ALTER TABLE projection_scenarios
    DROP COLUMN IF EXISTS include_future_expenses,
    DROP COLUMN IF EXISTS from_purchase,
    DROP COLUMN IF EXISTS monthly_expenses;
