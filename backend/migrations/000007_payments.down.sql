DROP INDEX IF EXISTS payment_schedules_org_due_idx;
DROP INDEX IF EXISTS payment_schedules_org_status_due_idx;

DROP TABLE IF EXISTS payment_allocations;

DROP INDEX IF EXISTS payments_org_paid_at_idx;

ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_reversal_check;
ALTER TABLE payments
    DROP COLUMN IF EXISTS reversed_by_user_id,
    DROP COLUMN IF EXISTS reversal_reason,
    DROP COLUMN IF EXISTS reversed_at;
