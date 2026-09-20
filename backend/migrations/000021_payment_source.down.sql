-- Reverses 000021. Dropping the column loses only the distinction between
-- `manual` and `backfill`; `import` round-trips, because `import_batch_id` is
-- still what it was derived from.
DROP INDEX IF EXISTS payments_org_source_idx;
ALTER TABLE payments DROP COLUMN IF EXISTS source;
