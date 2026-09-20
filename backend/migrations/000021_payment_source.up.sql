-- Phase 20 §20.3 — where a payment came from.
--
-- Until now "imported" was derived from `payments.import_batch_id` alone, which
-- answered one question (which spreadsheet?) and could not answer the one the
-- backfill introduces: was this money keyed in as it arrived, loaded from a
-- file, or written to close a period that predates TMS? `source` is that
-- answer, and it is what the ledger filter and the "imported"/"backfilled"
-- chips read. `import_batch_id` stays: it still names the batch and is still
-- what the 24 h undo walks.

ALTER TABLE payments
    ADD COLUMN source TEXT NOT NULL DEFAULT 'manual'
        CHECK (source IN ('manual', 'import', 'backfill'));

-- Every row that arrived on a CSV import is an `import` row: the derived chip
-- becomes the stored value, so the two can never disagree afterwards.
UPDATE payments SET source = 'import' WHERE import_batch_id IS NOT NULL;

-- `GET /payments?source=` is always org-scoped, and the ledger reads the newest
-- first, so the index carries the order the listing uses.
CREATE INDEX payments_org_source_idx ON payments (org_id, source, paid_at DESC)
    WHERE deleted_at IS NULL;
