DROP TABLE IF EXISTS projection_scenarios;
ALTER TABLE expense_categories DROP COLUMN IF EXISTS is_capital;
ALTER TABLE properties
    DROP COLUMN IF EXISTS current_value,
    DROP COLUMN IF EXISTS purchase_date,
    DROP COLUMN IF EXISTS purchase_price;
