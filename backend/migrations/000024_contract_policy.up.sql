-- Phase 22 §22.2 — the rules a tenancy is signed under.
--
-- `contract_templates.policy` is what the landlord configures; `contracts.policy`
-- is the copy taken when the contract is written (deposit resolved to an
-- amount) and is covered by `snapshot_hash`. NULL on both means "no policy":
-- every contract written before this migration, and any template that sets none.
ALTER TABLE contract_templates ADD COLUMN policy JSONB;
ALTER TABLE contracts          ADD COLUMN policy JSONB;
