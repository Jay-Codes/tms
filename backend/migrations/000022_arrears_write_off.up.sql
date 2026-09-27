-- Phase 21 §21.2 — money owed after a tenancy has closed.
--
-- A terminated or ended contract keeps the rows the renter lived through
-- (FLOWS 6.5). Until now nothing could happen to them: payments were refused
-- once the contract stopped running, and the debt sat there with no way to
-- collect it or to close it. Payments are now accepted against those rows, and
-- `written_off` is the landlord's decision that the rest will never come in.
--
-- `written_off` is not `waived`. A waiver says "this was never owed" (a
-- termination's unlived periods, a backfill of forgiven history) and drops out
-- of `expected`. A write-off says "this was owed and was not paid": it stays in
-- `expected`, leaves `outstanding`, and the collection rate shows the loss.

ALTER TABLE payment_schedules DROP CONSTRAINT payment_schedules_status_check;
ALTER TABLE payment_schedules ADD CONSTRAINT payment_schedules_status_check
    CHECK (status IN ('pending', 'paid', 'partial', 'overdue', 'waived', 'written_off'));

ALTER TABLE payment_schedules
    ADD COLUMN written_off_at         TIMESTAMPTZ,
    ADD COLUMN written_off_by_user_id UUID REFERENCES users (id),
    ADD COLUMN write_off_reason       TEXT CHECK (char_length(write_off_reason) <= 200);

-- The former-tenants list reads closed contracts that still owe; this keeps the
-- per-org scan off the full schedule table.
CREATE INDEX payment_schedules_owing_idx ON payment_schedules (org_id, contract_id)
    WHERE status IN ('pending', 'partial', 'overdue') AND deleted_at IS NULL;
