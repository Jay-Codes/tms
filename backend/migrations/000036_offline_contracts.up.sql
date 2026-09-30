-- Phase 30 — offline contracts for backfill.
--
-- A landlord onboarding a renter mid-tenancy holds the paper contract, not a
-- TMS one. A backfill given `from` now records an OFFLINE CONTRACT for the real
-- renter and unit: status `ended`, never signed or issued in TMS, with the
-- periods the backfill settles. It replaces Phase 29's history periods on the
-- running contract, which stay readable (and undoable) for batches that exist.
--
--   * `contracts.is_offline`: flags such a contract everywhere it is shown;
--   * `backfill_batches.created_contract_id`: the offline contract a batch
--     wrote, so its undo removes it again.

ALTER TABLE contracts
    ADD COLUMN is_offline BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE backfill_batches
    ADD COLUMN created_contract_id UUID REFERENCES contracts (id);
CREATE INDEX backfill_batches_created_contract_idx ON backfill_batches (created_contract_id)
    WHERE created_contract_id IS NOT NULL;
