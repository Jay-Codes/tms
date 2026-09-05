-- Phase 4: contracts, digital signatures, payment schedules, branding assets.
--
-- Only what the Phase 4 contract needs on top of the 000002 schema:
--   * contracts.activated_at / terminated_at / termination_reason /
--     termination_effective_date — the lifecycle timestamps the document and
--     the audit trail read back (SPEC §5.5, FLOWS 6.5).
--   * contracts.link_request_id — an approval creates the contract, and the
--     backfill path needs to know which request a contract came from so a
--     second approval cannot create a second contract.
--   * payment_schedules.paid_amount — Phase 5 records payments against it; the
--     schedule read endpoints already surface it, so the column lands with the
--     rows it belongs to rather than after them.
--   * a default contract template per org, seeded here for orgs that already
--     exist (org creation seeds its own from Phase 4 onward).

-- ---------------------------------------------------------------- contracts --

ALTER TABLE contracts
    ADD COLUMN activated_at               TIMESTAMPTZ,
    ADD COLUMN terminated_at              TIMESTAMPTZ,
    ADD COLUMN termination_reason         TEXT,
    -- The day a termination takes effect: schedules starting after it are
    -- waived, earlier ones stand (the renter lived there).
    ADD COLUMN termination_effective_date DATE,
    ADD COLUMN link_request_id            UUID REFERENCES unit_link_requests (id) ON DELETE SET NULL;

CREATE INDEX contracts_link_request_idx ON contracts (link_request_id)
    WHERE link_request_id IS NOT NULL AND deleted_at IS NULL;

-- The lifecycle job sweeps by end_date across the live statuses.
CREATE INDEX contracts_lifecycle_idx ON contracts (end_date)
    WHERE status IN ('active', 'expiring') AND deleted_at IS NULL;
CREATE INDEX contracts_org_status_idx ON contracts (org_id, status) WHERE deleted_at IS NULL;

-- One live contract per unit. The 409 on POST /contracts is enforced by the
-- database, not only by a check-then-insert two landlords could race through.
CREATE UNIQUE INDEX contracts_unit_live_key ON contracts (unit_id)
    WHERE status IN ('pending_signature', 'active', 'expiring') AND deleted_at IS NULL;

-- due_day snaps a due date to a day of the month, clamped to the month's
-- length (the 31st falls on the 30th in April) — internal/contract.Generate
-- already does exactly that, so the column need not stop at 28.
ALTER TABLE contracts DROP CONSTRAINT contracts_due_day_check;
ALTER TABLE contracts ADD CONSTRAINT contracts_due_day_check
    CHECK (due_day IS NULL OR (due_day BETWEEN 1 AND 31));

-- -------------------------------------------------------- payment_schedules --

ALTER TABLE payment_schedules
    ADD COLUMN paid_amount BIGINT NOT NULL DEFAULT 0 CHECK (paid_amount >= 0);

-- ------------------------------------------------------- contract_templates --

-- A template is soft-deleted (000002 already carries deleted_at); an org has at
-- most one default among its live templates.
CREATE UNIQUE INDEX contract_templates_default_key ON contract_templates (org_id)
    WHERE is_default AND deleted_at IS NULL;

-- Seed the standard template for every org that has none. Idempotent: re-running
-- the migration (or running it on a database seeded by org creation) inserts
-- nothing, because the NOT EXISTS covers any live template.
INSERT INTO contract_templates (org_id, name, body_html, is_default)
SELECT o.id, 'Standard tenancy agreement',
$tpl$<h1>Tenancy Agreement</h1>
<p>This agreement is made between <strong>{{org_name}}</strong> ("the Landlord") and <strong>{{renter_name}}</strong> ("the Tenant") for the premises known as <strong>{{unit}}</strong> at <strong>{{property}}</strong>.</p>
<h2>1. Term</h2>
<p>The tenancy runs for {{term_days}} days, from {{start_date}} to {{end_date}}.</p>
<h2>2. Rent</h2>
<p>The Tenant shall pay rent of <strong>{{rent}}</strong> per {{payment_period}}, payable in advance on or before day {{due_day}} of each payment period, to the bank account nominated by the Landlord. Receipts are issued for every payment.</p>
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
<p>Signed by both parties as recorded in the signature block below.</p>$tpl$,
       true
FROM orgs o
WHERE o.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM contract_templates t
      WHERE t.org_id = o.id AND t.deleted_at IS NULL
  );

-- ---------------------------------------------------------- notification_log --

-- Phase 4 sends three more kinds; the scheduler kinds arrive in Phase 6.
ALTER TABLE notification_log DROP CONSTRAINT notification_log_kind_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_kind_check
    CHECK (kind IN ('reminder_7d', 'reminder_due', 'overdue_daily', 'thank_you',
                    'otp', 'custom', 'link_approved', 'link_rejected',
                    'contract_ready', 'welcome', 'contract_terminated'));
