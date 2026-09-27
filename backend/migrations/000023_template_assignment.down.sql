-- Reverses 000023: every unit and property goes back to the org default.
DROP INDEX IF EXISTS units_template_idx;
DROP INDEX IF EXISTS properties_template_idx;
ALTER TABLE units      DROP COLUMN IF EXISTS contract_template_id;
ALTER TABLE properties DROP COLUMN IF EXISTS contract_template_id;
