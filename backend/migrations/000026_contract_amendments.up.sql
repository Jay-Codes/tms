-- Phase 22 §22.4 — changing a signed tenancy (PLAN2 Phase 17, supersession).
--
-- A signed document is never edited. An amendment is a new contract, pre-filled
-- from the running one with the changes applied, that the renter signs again.
-- The running contract keeps collecting until the amendment activates; from the
-- amendment's effective date its periods are waived, money already paid on
-- them moves to the new contract as credit, and it ends the day before.
--
--   amendment_effective_date  set only on an amendment: the first day it governs
--   amendment_reason          the landlord's one line on why
--   superseded_by_contract_id set on the old contract when the amendment activates
ALTER TABLE contracts
    ADD COLUMN amendment_effective_date  DATE,
    ADD COLUMN amendment_reason          TEXT CHECK (char_length(amendment_reason) <= 200),
    ADD COLUMN superseded_by_contract_id UUID REFERENCES contracts (id);

-- An amendment lives beside the contract it amends on the same unit until its
-- effective date, so the one-live-contract-per-unit rule excludes amendments.
-- Only one amendment may be open per contract at a time.
DROP INDEX contracts_unit_live_key;
CREATE UNIQUE INDEX contracts_unit_live_key ON contracts (unit_id)
    WHERE status IN ('pending_signature', 'active', 'expiring') AND deleted_at IS NULL
      AND amendment_effective_date IS NULL;
CREATE UNIQUE INDEX contracts_one_open_amendment ON contracts (supersedes_contract_id)
    WHERE status = 'pending_signature' AND amendment_effective_date IS NOT NULL
      AND deleted_at IS NULL;
