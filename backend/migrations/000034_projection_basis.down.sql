ALTER TABLE projection_scenarios ADD COLUMN occupancy_pct DOUBLE PRECISION;
UPDATE projection_scenarios SET occupancy_pct = 100 WHERE basis = 'best_case';
ALTER TABLE projection_scenarios
    DROP COLUMN IF EXISTS unit_ids,
    DROP COLUMN IF EXISTS basis;
