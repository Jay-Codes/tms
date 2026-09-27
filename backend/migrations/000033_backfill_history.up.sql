-- Phase 29 — a backfill may reach back before the contract's start date.
--
-- A landlord who joins TMS mid-tenancy often signs a fresh contract dated the
-- day they onboarded, while the real tenancy (and the paper contract that
-- proves it) began months earlier. Rather than making them terminate and
-- re-sign, a backfill can now generate the missing periods from the real
-- move-in (`from`) up to the contract's start date, at the rent charged back
-- then, and settle them in the same call.
--
--   * `payment_schedules.created_by_backfill_id`: the periods a batch wrote.
--     An undo removes them (soft delete) after reversing their money;
--   * `backfill_batches.from_date` / `created_periods`: what the Backfills
--     list shows, and what tells an undo there are rows to remove.

ALTER TABLE payment_schedules
    ADD COLUMN created_by_backfill_id UUID REFERENCES backfill_batches (id);
CREATE INDEX payment_schedules_created_by_backfill_idx ON payment_schedules (created_by_backfill_id)
    WHERE created_by_backfill_id IS NOT NULL;

ALTER TABLE backfill_batches
    ADD COLUMN from_date       DATE,
    ADD COLUMN created_periods INT NOT NULL DEFAULT 0;
