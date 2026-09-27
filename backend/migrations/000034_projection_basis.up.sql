-- Projections rework (27 Sep 2026, client walk-through of Phase 28).
--
-- The occupancy percentage was a guess the rent book cannot back. A scenario
-- now says which units it assumes let beyond their signed contracts:
--   * `basis`: 'contracts' (signed contracts only), 'selected' (plus the
--     units in `unit_ids`) or 'best_case' (every lettable unit, with contracts
--     assumed renewed at the unit's price).
--   * `unit_ids`: the picked units, used only by 'selected'. Not a foreign key:
--     a scenario outlives a deleted unit, which the projection then ignores.
-- Saved scenarios that set an occupancy become best case (the closest intent);
-- the rest keep to signed contracts.

ALTER TABLE projection_scenarios
    ADD COLUMN basis    TEXT NOT NULL DEFAULT 'contracts'
                        CHECK (basis IN ('contracts', 'selected', 'best_case')),
    ADD COLUMN unit_ids UUID[] NOT NULL DEFAULT '{}';

UPDATE projection_scenarios SET basis = 'best_case' WHERE occupancy_pct IS NOT NULL;

ALTER TABLE projection_scenarios DROP COLUMN occupancy_pct;
