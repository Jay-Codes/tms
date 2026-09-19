-- Phase 16 §16.2: CSV import of previous records.
--
-- An import is a two-step movement: a preview writes nothing but the batch and
-- its rows (what the file said, and what each line would become), and a commit
-- runs the ok rows through the ordinary service paths inside one transaction.
-- The batch is therefore a durable record of a spreadsheet, kept after the
-- commit so an undo within 24 h knows exactly what to take back.
--
--   import_batches  one uploaded file, its counts and its lifecycle
--   import_rows     one data line: its raw cells, its errors, and — after the
--                   commit — the entity it became
--
-- `payments.import_batch_id` is what makes the undo precise and what puts the
-- "imported" chip on a ledger row: a payment knows the spreadsheet it came in
-- on, and nothing else about the import touches the payments table.

-- ------------------------------------------------------------ import_batches --

CREATE TABLE import_batches (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    kind               TEXT NOT NULL CHECK (kind IN ('units', 'renters', 'payments')),
    filename           TEXT NOT NULL DEFAULT '',
    row_count          INT NOT NULL DEFAULT 0,
    ok_count           INT NOT NULL DEFAULT 0,
    error_count        INT NOT NULL DEFAULT 0,
    status             TEXT NOT NULL DEFAULT 'previewed'
                       CHECK (status IN ('previewed', 'committed', 'undone')),
    created_by_user_id UUID REFERENCES users (id),
    committed_at       TIMESTAMPTZ,
    undone_at          TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ
);

-- The history list pages newest first inside one org.
CREATE INDEX import_batches_org_created_idx ON import_batches (org_id, created_at DESC);

CREATE TRIGGER import_batches_set_updated_at BEFORE UPDATE ON import_batches
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- -------------------------------------------------------------- import_rows --

-- `raw` holds the file's cells verbatim, keyed by the machine column name: a
-- cell that opens with `=` is stored exactly as typed and neutralised on export
-- (the existing CSV rule), never on the way in.
--
-- `resolved` is what the preview decided the row would hit or create, so
-- GET /imports/{id} can re-serve the preview table without re-reading the file.
CREATE TABLE import_rows (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_id    UUID NOT NULL REFERENCES import_batches (id) ON DELETE CASCADE,
    org_id      UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    line        INT NOT NULL,
    raw         JSONB NOT NULL DEFAULT '{}'::jsonb,
    errors      JSONB,
    resolved    JSONB,
    entity_type TEXT,
    entity_id   UUID,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX import_rows_batch_line_idx ON import_rows (batch_id, line);
-- Every read of a row is org-scoped like the rest of the API (SPEC §2.1).
CREATE INDEX import_rows_org_batch_idx ON import_rows (org_id, batch_id, line);

CREATE TRIGGER import_rows_set_updated_at BEFORE UPDATE ON import_rows
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ------------------------------------------------------------------ payments --

ALTER TABLE payments
    ADD COLUMN import_batch_id UUID REFERENCES import_batches (id);

-- The undo walks one batch's payments; nothing else queries the column.
CREATE INDEX payments_import_batch_idx ON payments (import_batch_id)
    WHERE import_batch_id IS NOT NULL;
