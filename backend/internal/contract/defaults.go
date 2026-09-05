package contract

// DefaultTemplateBody is the body_html of the template seeded for every org
// ("Standard tenancy agreement"): short, plain Tanzanian residential tenancy
// terms using every variable the editor offers.
//
// Each migration that re-words the default carries a byte-identical copy for
// orgs that already existed (000005 seeded it, 000006 fixed the `{{due_day}}`
// phrase, 000012 added `{{rent_basis}}`); TestDefaultTemplateBodyMatchesMigration
// checks the newest of them against this constant, so a wording change cannot
// leave old and new orgs with different terms.
const DefaultTemplateBody = `<h1>Tenancy Agreement</h1>
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
<p>Signed by both parties as recorded in the signature block below.</p>`

// DefaultTemplateBodySW is the Swahili twin of DefaultTemplateBody: the
// `body_html_sw` seeded for every org, in the register a Tanzanian tenancy
// agreement is actually written in ("Mkataba wa Upangaji", "Mwenye Nyumba",
// "Mpangaji", "Kodi"). It carries exactly the same `{{variables}}`, so a
// document renders identically from either body.
//
// Migration 000015 seeds it for the orgs that already existed, byte-identical
// to this constant; TestDefaultTemplateBodySWMatchesMigration checks the two
// against each other, so old and new orgs cannot end up with different Swahili
// terms.
const DefaultTemplateBodySW = `<h1>Mkataba wa Upangaji</h1>
<p>Mkataba huu unafanywa kati ya <strong>{{org_name}}</strong> ("Mwenye Nyumba") na <strong>{{renter_name}}</strong> ("Mpangaji") kwa eneo linalojulikana kama <strong>{{unit}}</strong> katika <strong>{{property}}</strong>.</p>
<h2>1. Muda wa upangaji</h2>
<p>Upangaji utadumu kwa siku {{term_days}}, kuanzia {{start_date}} hadi {{end_date}}.</p>
<h2>2. Kodi</h2>
<p>Mpangaji atalipa kodi ya <strong>{{rent}}</strong> kwa kila {{payment_period}} ({{rent_basis}}), ikilipwa kabla au ifikapo {{due_day}} ya kila kipindi cha malipo, katika akaunti ya benki iliyotajwa na Mwenye Nyumba. Risiti hutolewa kwa kila malipo.</p>
<h2>3. Dhamana na huduma</h2>
<p>Dhamana yoyote iliyoshikiliwa itarejeshwa mwishoni mwa upangaji, baada ya kutolewa gharama za kurekebisha uharibifu unaozidi uchakavu wa kawaida. Gharama za umeme, maji na taka za eneo hili zitalipwa na Mpangaji isipokuwa pale ambapo imekubaliwa vinginevyo kwa maandishi.</p>
<h2>4. Matumizi ya eneo</h2>
<p>Mpangaji atatumia eneo kwa makazi tu, hatapangisha wala kuhamisha mkataba huu kwa mtu mwingine bila idhini ya maandishi ya Mwenye Nyumba, na atatunza eneo likiwa safi na katika hali nzuri.</p>
<h2>5. Matengenezo na ukaguzi</h2>
<p>Mwenye Nyumba atatunza muundo, paa na mifumo ya jengo katika hali nzuri. Mpangaji ataarifu kasoro mara moja na atamruhusu Mwenye Nyumba kuingia katika saa za kawaida, kwa taarifa ya kutosha, kukagua au kufanya matengenezo.</p>
<h2>6. Kusitisha upangaji</h2>
<p>Upande wowote unaweza kusitisha mkataba huu kwa kutoa taarifa ya maandishi ya siku thelathini (30). Mwenye Nyumba anaweza kuusitisha mara moja pale kodi inapobaki bila kulipwa kwa siku thelathini (30) baada ya tarehe ya malipo, au pale Mpangaji anapovunja masharti haya.</p>
<h2>7. Sheria</h2>
<p>Mkataba huu unaongozwa na sheria za Jamhuri ya Muungano wa Tanzania, pamoja na masharti ya Mwenye Nyumba na Mpangaji yaliyomo katika Sheria ya Ardhi na sheria za Udhibiti wa Kodi zinazotumika.</p>
<p>Umesainiwa na pande zote mbili kama ilivyoandikwa katika sehemu ya saini hapa chini.</p>`
