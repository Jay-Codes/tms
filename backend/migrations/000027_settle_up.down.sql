ALTER TABLE contracts DROP COLUMN IF EXISTS settlement;
DELETE FROM payments WHERE method = 'deposit';
ALTER TABLE payments DROP CONSTRAINT payments_method_check;
ALTER TABLE payments ADD CONSTRAINT payments_method_check
    CHECK (method IN ('cash', 'bank_transfer', 'mobile_money_manual', 'gateway'));
DROP TABLE IF EXISTS deposit_entries;
DROP TABLE IF EXISTS rent_refund_items;
DROP TABLE IF EXISTS rent_refunds;
