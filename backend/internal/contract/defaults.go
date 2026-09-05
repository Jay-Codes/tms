package contract

// DefaultTemplateBody is the body_html of the template seeded for every org
// ("Standard tenancy agreement"): short, plain Tanzanian residential tenancy
// terms using every variable the editor offers.
//
// Migration 000005 carries a byte-identical copy for orgs that already existed
// when Phase 4 landed; TestDefaultTemplateBodyMatchesMigration keeps the two in
// step, so a wording change cannot leave old and new orgs with different terms.
const DefaultTemplateBody = `<h1>Tenancy Agreement</h1>
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
<p>Signed by both parties as recorded in the signature block below.</p>`
