-- Phase 13: Swahili/English per user (PLAN2 Phase 13, SPEC §3.2).
--
-- `users.locale` already exists (000012). What is missing is the record of the
-- language each message and each document actually went out in, and the second
-- body a bilingual org needs:
--
--   1. `notification_log.language` — the language one SMS was rendered in.
--      The renter's locale decides it (falling back to the org's
--      `sms_language`), so the column is the only place the answer is written
--      down: the body itself is prose, and "which language did Asha get?" must
--      be answerable from the delivery log without guessing at the words.
--   2. `contract_templates.body_html_sw` — the Swahili twin of `body_html`,
--      which stays the English body. Empty means "this org has no Swahili
--      terms": rendering then falls back to English rather than issuing a
--      blank document.
--   3. `contracts.language` — the language a contract was rendered in, frozen
--      beside the terms it snapshots. Existing contracts default to 'en',
--      which is what they were rendered from: the sole template body was the
--      English one.
--
-- The snapshot flow is untouched: `snapshot_hash` still covers the rendered
-- HTML, so a contract issued in Swahili verifies exactly as an English one does.

-- ------------------------------------------------ notification_log.language --

ALTER TABLE notification_log
    ADD COLUMN language TEXT NOT NULL DEFAULT 'sw'
        CHECK (language IN ('sw', 'en'));

-- ------------------------------------- contract_templates.body_html_sw --

ALTER TABLE contract_templates
    ADD COLUMN body_html_sw TEXT NOT NULL DEFAULT '';

-- Every org whose default template still carries the platform wording verbatim
-- gains the Swahili twin of it (byte-identical to
-- contract.DefaultTemplateBodySW; TestDefaultTemplateBodySWMatchesMigration
-- checks this migration against the constant). A body the landlord has since
-- edited is left alone: its Swahili counterpart is theirs to write, and
-- guessing at one would put words the landlord never approved into a contract.
UPDATE contract_templates
SET body_html_sw =
$tplsw$<h1>Mkataba wa Upangaji</h1>
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
<p>Umesainiwa na pande zote mbili kama ilivyoandikwa katika sehemu ya saini hapa chini.</p>$tplsw$
WHERE body_html_sw = ''
  AND body_html LIKE '%per {{payment_period}} ({{rent_basis}}), payable in advance%';

-- --------------------------------------------------------- contracts.language --

-- 'en' for the rows that already exist: `body_html` was the only body there
-- was, and it is English. New contracts write the language explicitly.
ALTER TABLE contracts
    ADD COLUMN language TEXT NOT NULL DEFAULT 'en'
        CHECK (language IN ('sw', 'en'));
