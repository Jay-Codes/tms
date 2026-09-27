-- Phase 26 — a backfill is one decision, so it is one row that can be undone.
--
--   * `backfill_batches`: one row per POST /contracts/{id}/backfill that
--     settled something (or per line of a `backfill` CSV import), carrying
--     the counts the contract page lists and the undo stamp;
--   * `payments.backfill_batch_id`: the payments a `paid` batch wrote;
--   * `payment_schedules.backfill_batch_id`: the rows a `waived` batch closed
--     — a waiver has no payment to hang the batch on;
--   * import kind `backfill`.

CREATE TABLE backfill_batches (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    contract_id        UUID NOT NULL REFERENCES contracts (id),
    mode               TEXT NOT NULL CHECK (mode IN ('paid', 'waived')),
    until              DATE NOT NULL,
    periods            INT NOT NULL DEFAULT 0,
    amount             BIGINT NOT NULL DEFAULT 0,
    import_batch_id    UUID REFERENCES import_batches (id),
    created_by_user_id UUID REFERENCES users (id),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    undone_at          TIMESTAMPTZ,
    undone_by_user_id  UUID REFERENCES users (id),
    undo_reason        TEXT CHECK (char_length(undo_reason) <= 500)
);
CREATE INDEX backfill_batches_contract_idx ON backfill_batches (org_id, contract_id, created_at DESC);
CREATE INDEX backfill_batches_import_idx ON backfill_batches (import_batch_id)
    WHERE import_batch_id IS NOT NULL;

ALTER TABLE payments ADD COLUMN backfill_batch_id UUID REFERENCES backfill_batches (id);
CREATE INDEX payments_backfill_batch_idx ON payments (backfill_batch_id)
    WHERE backfill_batch_id IS NOT NULL;

ALTER TABLE payment_schedules ADD COLUMN backfill_batch_id UUID REFERENCES backfill_batches (id);
CREATE INDEX payment_schedules_backfill_batch_idx ON payment_schedules (backfill_batch_id)
    WHERE backfill_batch_id IS NOT NULL;

ALTER TABLE import_batches DROP CONSTRAINT import_batches_kind_check;
ALTER TABLE import_batches ADD CONSTRAINT import_batches_kind_check
    CHECK (kind IN ('units', 'renters', 'payments', 'backfill'));
