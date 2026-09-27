DROP INDEX IF EXISTS contracts_one_open_amendment;
DROP INDEX IF EXISTS contracts_unit_live_key;
CREATE UNIQUE INDEX contracts_unit_live_key ON contracts (unit_id)
    WHERE status IN ('pending_signature', 'active', 'expiring') AND deleted_at IS NULL;
ALTER TABLE contracts
    DROP COLUMN IF EXISTS superseded_by_contract_id,
    DROP COLUMN IF EXISTS amendment_reason,
    DROP COLUMN IF EXISTS amendment_effective_date;
