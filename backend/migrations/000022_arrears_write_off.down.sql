-- Reverses 000022. A written-off row goes back to `overdue` — it was past due
-- when it was written off, and the CHECK cannot narrow while one remains.
DROP INDEX IF EXISTS payment_schedules_owing_idx;
UPDATE payment_schedules SET status = 'overdue' WHERE status = 'written_off';
ALTER TABLE payment_schedules
    DROP COLUMN IF EXISTS write_off_reason,
    DROP COLUMN IF EXISTS written_off_by_user_id,
    DROP COLUMN IF EXISTS written_off_at;
ALTER TABLE payment_schedules DROP CONSTRAINT payment_schedules_status_check;
ALTER TABLE payment_schedules ADD CONSTRAINT payment_schedules_status_check
    CHECK (status IN ('pending', 'paid', 'partial', 'overdue', 'waived'));
