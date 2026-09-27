DROP INDEX IF EXISTS payment_schedules_backfill_batch_idx;
ALTER TABLE payment_schedules DROP COLUMN IF EXISTS backfill_batch_id;
DROP INDEX IF EXISTS payments_backfill_batch_idx;
ALTER TABLE payments DROP COLUMN IF EXISTS backfill_batch_id;
DROP TABLE IF EXISTS backfill_batches;
DELETE FROM import_rows WHERE batch_id IN (SELECT id FROM import_batches WHERE kind = 'backfill');
DELETE FROM import_batches WHERE kind = 'backfill';
ALTER TABLE import_batches DROP CONSTRAINT import_batches_kind_check;
ALTER TABLE import_batches ADD CONSTRAINT import_batches_kind_check
    CHECK (kind IN ('units', 'renters', 'payments'));
