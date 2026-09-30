DROP INDEX IF EXISTS backfill_batches_created_contract_idx;
ALTER TABLE backfill_batches DROP COLUMN IF EXISTS created_contract_id;
ALTER TABLE contracts DROP COLUMN IF EXISTS is_offline;
