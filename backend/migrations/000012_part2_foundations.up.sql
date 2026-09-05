-- Part 2 foundations (PLAN2 Phase 9).
--
-- One migration carries the whole Part 2 data model so the phases that follow
-- add endpoints rather than schema. Four things happen here:
--
--   1. `users.locale` — the per-user language Phase 13 reads (PLAN2: renter
--      locale drives every SMS to that renter, landlord locale drives their
--      screens; the org's `sms_language` stays only as the fallback).
--   2. The "recommended" payment period becomes singular per org: a data fix
--      that keeps one, then a partial unique index that keeps it that way.
--   3. The tables Phases 10, 12 and 14 fill: expenses, org themes, platform
--      templates, SMS credits. Empty for now, created together so a later
--      phase never has to migrate around live rows.
--   4. `notification_log.status` gains `held_no_credit` — the state a message
--      waits in when its org has no credit left (Phase 14), which is not a
--      failure and must not be retried as one.

-- ------------------------------------------------------------ users.locale --

-- 'sw' is the default because the platform's renters are Tanzanian and
-- Kiswahili is what a landlord's SMS already goes out in; a user who wants
-- English says so.
ALTER TABLE users
    ADD COLUMN locale TEXT NOT NULL DEFAULT 'sw'
        CHECK (locale IN ('sw', 'en'));

-- ------------------------------------------- one recommended period per org --

-- Every org bootstrapped before this migration flagged all four presets
-- `is_recommended`, which put the badge on every row and so told the renter
-- nothing (PLAN2 #8). The badge now means "the one this landlord suggests".
--
-- The survivor is the smallest `days` among an org's live recommended periods —
-- Monthly for every org that kept its bootstrap — with the lowest `sort_order`
-- breaking a tie and the id breaking that. Soft-deleted rows keep whatever they
-- carried: they are history, and the index ignores them.
UPDATE payment_periods p
SET is_recommended = false
WHERE p.is_recommended
  AND p.deleted_at IS NULL
  AND p.id <> (
      SELECT keep.id
      FROM payment_periods keep
      WHERE keep.org_id = p.org_id
        AND keep.is_recommended
        AND keep.deleted_at IS NULL
      ORDER BY keep.days ASC, keep.sort_order ASC, keep.id ASC
      LIMIT 1
  );

CREATE UNIQUE INDEX payment_periods_one_recommended_per_org
    ON payment_periods (org_id)
    WHERE is_recommended AND deleted_at IS NULL;

-- ------------------------------------------------------- expense categories --

-- Per-org, because "Utilities" means what each landlord decides it means.
-- `is_default` marks the seeded set so a UI can offer "restore defaults" the
-- way payment periods already do.
CREATE TABLE expense_categories (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    is_default  BOOLEAN NOT NULL DEFAULT false,
    sort_order  INT NOT NULL DEFAULT 0,
    active      BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ
);
CREATE INDEX expense_categories_org_id_idx ON expense_categories (org_id);
CREATE UNIQUE INDEX expense_categories_org_name_key
    ON expense_categories (org_id, lower(name)) WHERE deleted_at IS NULL;
CREATE TRIGGER expense_categories_set_updated_at BEFORE UPDATE ON expense_categories
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ------------------------------------------------------------------ expenses --

-- Property-level ledger with an optional unit (PLAN2 Phase 10). Corrections are
-- append-style like payments: an expense is voided with a reason, never edited
-- into nothing.
CREATE TABLE expenses (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id              UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    property_id         UUID NOT NULL REFERENCES properties (id) ON DELETE CASCADE,
    unit_id             UUID REFERENCES units (id) ON DELETE SET NULL,
    category_id         UUID REFERENCES expense_categories (id) ON DELETE SET NULL,
    amount              BIGINT NOT NULL CHECK (amount > 0),
    incurred_on         DATE NOT NULL,
    vendor              TEXT NOT NULL DEFAULT '',
    reference           TEXT NOT NULL DEFAULT '',
    note                TEXT NOT NULL DEFAULT '',
    receipt_object_key  TEXT,
    recorded_by_user_id UUID REFERENCES users (id) ON DELETE SET NULL,
    status              TEXT NOT NULL DEFAULT 'recorded'
                        CHECK (status IN ('recorded', 'voided')),
    voided_at           TIMESTAMPTZ,
    void_reason         TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at          TIMESTAMPTZ
);
-- Both indexes lead with org_id (SPEC §2.1) and carry the date the ledger and
-- the Phase 11 series are read by; the second serves the per-property tab.
CREATE INDEX expenses_org_incurred_idx
    ON expenses (org_id, incurred_on) WHERE deleted_at IS NULL;
CREATE INDEX expenses_org_property_incurred_idx
    ON expenses (org_id, property_id, incurred_on) WHERE deleted_at IS NULL;
CREATE TRIGGER expenses_set_updated_at BEFORE UPDATE ON expenses
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------- org_themes --

-- One theme per org (Phase 12): either a preset id, or an explicit token set,
-- or both (a preset with a handful of advanced overrides on top). The org_id is
-- the primary key because a second row could only disagree with the first.
CREATE TABLE org_themes (
    org_id     UUID PRIMARY KEY REFERENCES orgs (id) ON DELETE CASCADE,
    preset_id  TEXT,
    tokens     JSONB NOT NULL DEFAULT '{}'::jsonb,
    font_id    TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TRIGGER org_themes_set_updated_at BEFORE UPDATE ON org_themes
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- --------------------------------------------------------- platform templates --

-- The platform-wide SMS wording, in both languages, editable by an admin
-- (Phase 14). `kind` is the primary key: there is exactly one platform default
-- per message kind. `locked` freezes a kind against org overrides — `otp` ships
-- locked, because a landlord rewording a security code is a phishing surface.
CREATE TABLE platform_templates (
    kind                TEXT PRIMARY KEY,
    sw                  TEXT NOT NULL,
    en                  TEXT NOT NULL,
    variables           TEXT[] NOT NULL DEFAULT '{}',
    locked              BOOLEAN NOT NULL DEFAULT false,
    updated_by_admin_id UUID REFERENCES users (id) ON DELETE SET NULL,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    version             INT NOT NULL DEFAULT 1
);
CREATE TRIGGER platform_templates_set_updated_at BEFORE UPDATE ON platform_templates
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- History, so an admin can see what a kind used to say and revert to it.
CREATE TABLE platform_template_versions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind          TEXT NOT NULL,
    version       INT NOT NULL,
    sw            TEXT NOT NULL,
    en            TEXT NOT NULL,
    admin_user_id UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (kind, version)
);
CREATE INDEX platform_template_versions_kind_idx
    ON platform_template_versions (kind, version DESC);

-- ------------------------------------------------------------- SMS credits --

-- Prepaid credit per org, set by the platform admin: no expiry, no monthly
-- reset (confirmed with the client). The balance may not go negative — the
-- worker's conditional `UPDATE … WHERE balance >= n` is the real guard, and
-- this CHECK is the backstop that makes a bug loud instead of silent.
CREATE TABLE org_sms_credits (
    org_id        UUID PRIMARY KEY REFERENCES orgs (id) ON DELETE CASCADE,
    balance       INT NOT NULL DEFAULT 0 CHECK (balance >= 0),
    low_watermark INT NOT NULL DEFAULT 50,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TRIGGER org_sms_credits_set_updated_at BEFORE UPDATE ON org_sms_credits
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- The ledger is the reconciliation record behind that balance, and it is
-- append-only for the same reason audit_log and contract_signatures are: a
-- balance nobody can reconstruct from its movements is not an accounting
-- record, and money is exactly where a correcting row beats an edited one.
CREATE TABLE sms_credit_ledger (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    delta           INT NOT NULL,
    balance_after   INT NOT NULL,
    reason          TEXT NOT NULL CHECK (reason IN ('topup', 'adjust', 'debit', 'refund')),
    notification_id UUID REFERENCES notification_log (id) ON DELETE SET NULL,
    admin_user_id   UUID REFERENCES users (id) ON DELETE SET NULL,
    note            TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX sms_credit_ledger_org_created_idx
    ON sms_credit_ledger (org_id, created_at DESC, id DESC);

CREATE OR REPLACE FUNCTION sms_credit_ledger_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'sms_credit_ledger is append-only: % is not permitted', TG_OP
        USING ERRCODE = 'insufficient_privilege';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER sms_credit_ledger_no_update BEFORE UPDATE ON sms_credit_ledger
    FOR EACH ROW EXECUTE FUNCTION sms_credit_ledger_append_only();
CREATE TRIGGER sms_credit_ledger_no_delete BEFORE DELETE ON sms_credit_ledger
    FOR EACH ROW EXECUTE FUNCTION sms_credit_ledger_append_only();

-- --------------------------------------- notification_log.held_no_credit --

-- A message an org cannot pay for is held, not failed: it goes out unchanged
-- when the admin tops the org up, and the retry machinery must leave it alone
-- in the meantime.
ALTER TABLE notification_log DROP CONSTRAINT notification_log_status_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_status_check
    CHECK (status IN ('queued', 'sending', 'sent', 'failed', 'held_no_credit'));

-- ----------------------------------------- contract_templates (rent re-word) --

-- Before Part 2 the document printed the unit's own price beside the payment
-- period label — "TZS 100,000 per Quarterly (90 days)" for a unit priced per 30
-- days — which is simply the wrong figure. `{{rent}}` now resolves to the amount
-- due once per payment period, and the new `{{rent_basis}}` carries the unit
-- price so both numbers appear.
--
-- Contracts already issued are untouched: their terms are a snapshot (SPEC
-- §5.5), and re-rendering one would change a document a renter has signed.
-- Only the templates future contracts are rendered from move.

-- Rows still carrying the default verbatim are replaced with the current
-- default body (byte-identical to contract.DefaultTemplateBody; the drift test
-- checks this migration against the constant).
UPDATE contract_templates
SET body_html =
$tpl$<h1>Tenancy Agreement</h1>
<p>This agreement is made between <strong>{{org_name}}</strong> ("the Landlord") and <strong>{{renter_name}}</strong> ("the Tenant") for the premises known as <strong>{{unit}}</strong> at <strong>{{property}}</strong>.</p>
<h2>1. Term</h2>
<p>The tenancy runs for {{term_days}} days, from {{start_date}} to {{end_date}}.</p>
<h2>2. Rent</h2>
<p>The Tenant shall pay rent of <strong>{{rent}}</strong> per {{payment_period}} ({{rent_basis}}), payable in advance on or before {{due_day}} of each payment period, to the bank account nominated by the Landlord. Receipts are issued for every payment.</p>
<h2>3. Deposit and utilities</h2>
<p>Any deposit held is refundable at the end of the tenancy, less the cost of repairing damage beyond fair wear and tear. Electricity, water and refuse charges for the premises are payable by the Tenant unless agreed otherwise in writing.</p>
<h2>4. Use of the premises</h2>
<p>The Tenant shall use the premises for residential purposes only, shall not sublet or assign without the Landlord's written consent, and shall keep the premises clean and in good order.</p>
<h2>5. Repairs and access</h2>
<p>The Landlord shall keep the structure, roof and installations in repair. The Tenant shall report defects promptly and shall allow the Landlord access at reasonable hours, on reasonable notice, to inspect or repair.</p>
<h2>6. Ending the tenancy</h2>
<p>Either party may end this tenancy by giving thirty (30) days' written notice. The Landlord may end it immediately where rent stays unpaid for thirty (30) days after the due date or where the Tenant breaches these terms.</p>
<h2>7. Law</h2>
<p>This agreement is governed by the laws of the United Republic of Tanzania, and by the Landlord and Tenant provisions of the Land Act and the Rent Restriction laws in force.</p>
<p>Signed by both parties as recorded in the signature block below.</p>$tpl$
WHERE body_html LIKE '%per {{payment_period}}, payable in advance%'
  AND body_html NOT LIKE '%<h1>Tenancy Agreement</h1><%';

-- Bodies a landlord has since edited (or that are stored sanitized and
-- newline-free) keep their own wording; only the rent sentence gains the basis.
UPDATE contract_templates
SET body_html = replace(body_html,
        'per {{payment_period}}, payable in advance',
        'per {{payment_period}} ({{rent_basis}}), payable in advance')
WHERE body_html LIKE '%per {{payment_period}}, payable in advance%';
