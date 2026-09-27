-- Phase 22 §22.1 — which contract template a unit's tenancy is written on.
--
-- Until now an org had one pool of templates and one default, and every
-- approved application was written on the default. A landlord with a shop
-- front and flats upstairs needs two agreements; one with a furnished block
-- needs its own clauses there. The template is now resolved, first hit wins:
--   the one picked at approval / creation → the unit's → the property's → the
--   org default.
-- Both columns are optional; NULL means "inherit". A soft-deleted template is
-- skipped by the resolver, and deleting one that is still assigned is refused.

ALTER TABLE properties ADD COLUMN contract_template_id UUID REFERENCES contract_templates (id);
ALTER TABLE units      ADD COLUMN contract_template_id UUID REFERENCES contract_templates (id);

CREATE INDEX properties_template_idx ON properties (org_id, contract_template_id)
    WHERE contract_template_id IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX units_template_idx ON units (org_id, contract_template_id)
    WHERE contract_template_id IS NOT NULL AND deleted_at IS NULL;
