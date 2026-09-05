-- Phase 4 leftovers:
--   * contract_signatures becomes append-only, like audit_log (000002). A
--     signature is evidence a party accepted a specific snapshot hash; editing
--     or deleting one after the fact would make the evidence worthless.
--   * template bodies seeded before the "{{due_day}} carries a phrase" fix are
--     brought to the current default wording. The old body said "on or before
--     day {{due_day}}", which renders as "day day 5" (or "day the first day")
--     now that DueDayPhrase supplies the whole phrase.

-- --------------------------------------------- contract_signatures (append-only) --

CREATE OR REPLACE FUNCTION contract_signatures_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'contract_signatures is append-only: % is not permitted', TG_OP
        USING ERRCODE = 'insufficient_privilege';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER contract_signatures_no_update BEFORE UPDATE ON contract_signatures
    FOR EACH ROW EXECUTE FUNCTION contract_signatures_append_only();
CREATE TRIGGER contract_signatures_no_delete BEFORE DELETE ON contract_signatures
    FOR EACH ROW EXECUTE FUNCTION contract_signatures_append_only();

-- ----------------------------------------------- contract_templates (re-word) --

-- Rows still carrying the old default verbatim are replaced with the current
-- default body (byte-identical to contract.DefaultTemplateBody; the drift test
-- checks both migrations against the constant).
UPDATE contract_templates
SET body_html =
$tpl$<h1>Tenancy Agreement</h1>
<p>This agreement is made between <strong>{{org_name}}</strong> ("the Landlord") and <strong>{{renter_name}}</strong> ("the Tenant") for the premises known as <strong>{{unit}}</strong> at <strong>{{property}}</strong>.</p>
<h2>1. Term</h2>
<p>The tenancy runs for {{term_days}} days, from {{start_date}} to {{end_date}}.</p>
<h2>2. Rent</h2>
<p>The Tenant shall pay rent of <strong>{{rent}}</strong> per {{payment_period}}, payable in advance on or before {{due_day}} of each payment period, to the bank account nominated by the Landlord. Receipts are issued for every payment.</p>
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
WHERE body_html LIKE '%on or before day {{due_day}} of each payment period%'
  AND body_html NOT LIKE '%<h1>Tenancy Agreement</h1><%';

-- Bodies a landlord has since edited (or that were stored in sanitized,
-- newline-free form) keep their own wording; only the broken phrase is fixed.
UPDATE contract_templates
SET body_html = replace(body_html, 'on or before day {{due_day}}', 'on or before {{due_day}}')
WHERE body_html LIKE '%on or before day {{due_day}}%';
