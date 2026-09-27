ALTER TABLE contracts          DROP COLUMN IF EXISTS supersedes_contract_id;
ALTER TABLE contract_templates DROP COLUMN IF EXISTS content_updated_at;
