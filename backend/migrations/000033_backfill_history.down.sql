ALTER TABLE backfill_batches
    DROP COLUMN IF EXISTS created_periods,
    DROP COLUMN IF EXISTS from_date;
DROP INDEX IF EXISTS payment_schedules_created_by_backfill_idx;
ALTER TABLE payment_schedules DROP COLUMN IF EXISTS created_by_backfill_id;
