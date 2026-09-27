-- Reverses 000024. Contracts written with a policy will no longer verify
-- (their hash covers it); rolling back past this point is for a fresh database.
ALTER TABLE contracts          DROP COLUMN IF EXISTS policy;
ALTER TABLE contract_templates DROP COLUMN IF EXISTS policy;
