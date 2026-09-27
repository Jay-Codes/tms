DROP TABLE IF EXISTS platform_sms_purchases;
DROP TABLE IF EXISTS snippe_webhook_events;
DROP TABLE IF EXISTS sms_credit_orders;
DROP TABLE IF EXISTS sms_credit_packages;
-- The ledger is append-only, so `purchase` rows already written cannot be
-- removed: the old CHECK comes back NOT VALID — it refuses new purchase rows
-- without failing on the history.
ALTER TABLE sms_credit_ledger DROP CONSTRAINT sms_credit_ledger_reason_check;
ALTER TABLE sms_credit_ledger ADD CONSTRAINT sms_credit_ledger_reason_check
    CHECK (reason IN ('topup', 'adjust', 'debit', 'refund')) NOT VALID;
