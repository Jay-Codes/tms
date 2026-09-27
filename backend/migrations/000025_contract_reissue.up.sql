-- Phase 22 §22.3 — unsigned contracts whose template has since changed.
--
-- A contract freezes its template's wording when it is written (snapshot
-- rule). While it waits for the renter's signature, the landlord may improve
-- the template; the renter would still sign the old text. `content_updated_at`
-- moves only when the wording or the policy changes (not a rename), so
-- "written before the template last changed" is a plain comparison, and a
-- reissue withdraws the old contract and writes a new one from the same terms.
ALTER TABLE contract_templates ADD COLUMN content_updated_at TIMESTAMPTZ;
UPDATE contract_templates SET content_updated_at = updated_at;
ALTER TABLE contract_templates ALTER COLUMN content_updated_at SET NOT NULL,
                               ALTER COLUMN content_updated_at SET DEFAULT now();

-- The new contract points at the one it replaced (Phase 17's supersession,
-- first used by reissue).
ALTER TABLE contracts ADD COLUMN supersedes_contract_id UUID REFERENCES contracts (id);
