-- Reverses 000019. The payments column goes first: it references the batches
-- table, and a rollback must leave payments exactly as 000018 left them.
DROP INDEX IF EXISTS payments_import_batch_idx;
ALTER TABLE payments DROP COLUMN IF EXISTS import_batch_id;

DROP TABLE IF EXISTS import_rows;
DROP TABLE IF EXISTS import_batches;
