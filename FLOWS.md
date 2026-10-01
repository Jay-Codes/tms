# FLOWS.md — TMS User Flows

Companion to [SPEC.md](SPEC.md). Actors: **Renter** (`apps/enduser`), **Landlord user** (`apps/tenant`), **Platform admin** (`apps/admin`), **System** (scheduler/jobs).

---

## 1. Landlord onboarding

1. Landlord opens signup link → registers org: business name, owner name, email, phone, password.
2. Email verification link → verified.
3. Guided setup wizard:
   1. **Branding** — display name (e.g. "JJnE Rentals"), logo upload, optional letterhead upload + document footer text (used on contract documents), and the **app theme**: pick one of the eight presets (Ledger, Night ledger, Warm paper, Cool slate, Forest, Ocean, High-contrast, Minimal white) from a gallery with a live mini-ledger preview, or open **Advanced** to set individual tokens (paper, surface, ink, ink-muted, rule, primary, accent) and the font. The gallery is served from the platform's own catalogue (`GET /themes/presets`), so what the landlord picks is exactly what the server will accept. Contrast badges warn live and a body-text pair below 4.5:1 is refused on save, with the failing pairs and their ratios named per swatch. The chosen theme applies to both the landlord app and the renter-facing pages; the admin app is never themed.
   2. **First property** — name (custom, e.g. "Mbezi Beach Block A"), location.
   3. **Payment periods** — list pre-seeded with four presets (Monthly 30d, Quarterly 90d, Half-year 180d, Yearly 365d). **Monthly** carries the single "Recommended" badge; "Set as recommended" moves it to any other period, and only one period can hold it. Landlord keeps/removes any, and adds custom periods as a label + number of days (e.g. "Weekly" 7d, "3 weeks" 21d, "45 days") — no limit on count or value.
   4. **Units** — add units with custom names ("Room 1", "House B"), set price (amount per N days, default 30) and optionally restrict which payment periods this unit offers.
   5. **Contract template** — start from default terms, edit; set default due day and grace days.
   6. **Notification settings** — confirm reminder timings, sender name, and the **default language for renters without a preference** (SW/EN) — each renter's own choice wins over it.
   7. **Language preference** — the owner's own screen language (Kiswahili / English), prefilled from the signup toggle and changeable later in Settings → Preferences. It also decides the language of anything the platform sends to them.
4. Wizard ends on dashboard; empty-state cards prompt "Print QR codes" and "Invite staff".
4a. **Payment instructions nudge.** Until the org has a bank account saved, the setup checklist and the dashboard carry "Add payment instructions so renters know where to pay" → Settings → Bank account, which shows a live **renter preview** of the card the renter will see, plus an optional **Mobile money** block (provider, number, name). The nudge is dismissable and comes back if the details are later cleared.
5. Optional: invite `org_manager` staff by email.

**Edge cases:** duplicate org email → resend verification; abandoning wizard → resumable, dashboard shows setup checklist.

---

## 2. Renter onboarding (QR-first)

1. Renter scans QR sticker on the house/unit → opens `/enduser/u/{unit_code}`.
2. Landing shows **org branding** (the org's theme applied) + property/unit name, rent price, terms summary. The page opens in the **org's language** with a visible **SW / EN toggle** in the header; the choice is remembered locally. CTA: "Register to connect" (or "Log in").
3. Register: phone number → OTP SMS (Beem, in the chosen language) → verify → set PIN. The language picked on the landing page is prefilled as the renter's own preference and saved on the account; it can be changed anytime in Profile, and from then on it decides both the app language and every SMS they receive.
4. KYC form: full name, **NIDA number**, next of kin (name + phone), contact number (prefilled), email. Optional ID photo upload.
5. Choose **payment period** from the landlord's offered list for this unit — the landlord's one **recommended** period is listed first and badged, the rest follow in the landlord's order (including custom ones like "21 days") — each showing the amount for that period. Choose **tenancy length** (term) and **start date** — end date auto-derived, shown, with the resulting schedule preview (N payments of X).
6. Review terms (from landlord's template) → accept → **link request** submitted.
   (Signing happens after approval — step 7b — so the renter signs the final document with the landlord-confirmed dates.)
7. Status screen: "Waiting for landlord approval" (skipped if org auto-approve on).
7b. On approval, SMS: "Your contract for {unit} is ready to sign." → renter opens the contract document (letterhead, full terms, schedule) → **Accept & sign**: OTP sent to registered phone → enter code → optional draw signature → signed. Status: "Waiting for landlord to countersign."
8. Landlord activates (countersigns) → SMS: "Welcome to {org}. Your tenancy at {unit} starts {date}." → renter dashboard live; signed contract available to print anytime.

**Alternate entries:** landlord manually adds renter (sends SMS invite link with the same flow, unit pre-linked); renter with existing account scans a new QR → jumps straight to step 5.

**Landlord-assisted entry (in person, no SMS)** — Flow 2b below. Used when the OTP text does not arrive (provider stuck, network blackhole, no credit) and landlord and renter are in the same room.

### 2b. Landlord-assisted onboarding (in person)

The landlord's phone becomes the code channel. Nothing is sent by SMS; the renter still acts on their **own** device, so the account, PIN and signature stay theirs.

1. Landlord (owner or manager): unit page → **Onboard in person**, or Renters → **Add renter** → "In person". Enters the renter's phone.
2. Backend opens an **assist session** for (org, unit, phone): a fresh 6-digit code stored under the same key the SMS path uses (`register`, or `login` when the number already has an account), **no SMS**. Returns the code, its expiry, and a link.
3. Landlord screen: big code, QR of the link, 5-min countdown, **New code** button, and the live status line "Waiting for renter…". The renter scans the QR (or the landlord reads the link) and lands on `/enduser/u/{unit_code}?assist={session_id}`.
4. Renter: enters their phone → the "Send code" step is skipped, straight to "Enter the code your landlord shows you" → PIN → KYC → period / term / start date → link request (Flow 2 steps 4–6, unchanged). An existing account is asked to log in with PIN, or with the shown code as the OTP fallback.
5. Landlord screen flips as the renter progresses: **Registered** → **Request received** (button "Review & approve", the ordinary Flow 3 approval) → **Approved**. Session ends 30 min after it was opened or when the landlord closes it.
6. Signing: landlord opens the contract → **Witness signing** → a signing code is shown on the landlord's screen (same key the `contract_sign_otp` SMS would fill) → renter enters it in **Accept & sign** on their own device → signature row `otp_accept` plus `witnessed_by`. The landlord cannot sign for the renter here — that stays the no-phone `landlord_recorded` path (Flow 3.6).

**Edge cases:** the SMS path and the assisted path share one code slot per (purpose, phone) — issuing an assisted code replaces any code still in flight, so a late-arriving text carries a dead code; code reveals and refreshes are audited per event (`renter.assist_code`); refreshes are limited per org (30/h) rather than per phone, because the landlord vouches for the number; the verify-attempt limit (5) is unchanged; a phone that belongs to a **staff/admin** account is refused (`not_a_renter_phone`); the public session lookup returns only the unit code and purpose, never the phone.


**Edge cases:** QR of occupied unit → show "unit occupied — contact landlord" unless landlord enabled waitlist; wrong unit scanned → renter cancels request; OTP retries rate-limited with resend cooldown.

---

## 3. Link approval & contract activation (landlord)

1. Dashboard badge: pending link requests.
2. Open request → renter KYC details, chosen duration, start date. NIDA shows masked; **"Show full number"** reveals it for 60 s after a confirm sheet (optional reason) — audited, rate-limited, and listed back to the renter in their profile. Same button on the renter detail page.
3. Approve → contract created from template (terms + price **snapshotted**, hash computed), status `pending_signature`, renter SMS'd to sign.
4. Reject (with reason) → renter notified by SMS.
5. Renter signs (OTP + optional drawn signature; or a **witnessed** code shown on the landlord's screen — Flow 2b step 6) → landlord sees "Ready to countersign" → **Activate** records the landlord signature, generates payment schedules for the whole span, unit → occupied.
6. Contract visible to both parties as an in-app document (org letterhead + logo, resolved terms, parties, schedule summary, signature block with names/timestamps/phone last-4/drawn signatures, verification hash). "Print / Save as PDF" uses the browser. Landlord can also **manually add a renter** and sign on their behalf only if the renter has no phone — flagged as `landlord_recorded` in the audit log (no renter signature row); avoid where possible.

**Edge cases:** renter doesn't sign within N days (org setting, default 7) → reminder SMS, then landlord can cancel; renter disputes → landlord terminates and issues a new contract (old one kept, never edited).

---

## 4. Vacancy management (landlord)

1. Units board filterable by status: **vacant / occupied / maintenance / unlisted**.
2. Contract ends or is terminated → unit auto-flips to vacant → appears on vacancy board.
3. Landlord can override status (e.g. maintenance) and reorder/rename units anytime — names are custom for quick reference.
4. Vacant unit's QR keeps working — next renter scans the same code.
5. Report tile: occupancy rate, vacant unit list with days-vacant.

---

## 5. Pricing management (landlord)

1. Unit page → "Prices" → current price (amount per N days) + full history.
2. New price with `effective_from` date → applies to **future contracts only**; active contracts keep their snapshotted rent.
2a. Org settings → "Payment periods": manage the list (add custom days, rename, reorder, deactivate; the four seeded presets are restorable) and choose which single period is badged **Recommended** — "Set as recommended" moves the badge, it is never on two periods at once, and the badged period is the one offered first to renters. Changes affect future contracts only.
3. Bulk price update across selected units (same %, or set amount).
4. Audit log records every price change (who, when, old → new).

---

## 6. Contracts & terms management (landlord)

1. Templates page: create/edit named templates in an in-app rich-text editor (headings, lists, bold, variables like `{{renter_name}}`, `{{rent}}`), set default. Live preview shows the document with the org's uploaded letterhead/logo.
1a. **Rent is written per payment period.** `{{rent}}` resolves to the amount due each payment period (unit price × payment-period days ÷ price-basis days, rounded as the schedule is), and `{{rent_basis}}` keeps the unit's own price — so a 100,000-per-30-days room on a quarterly cadence reads "TZS 300,000 per Quarterly (90 days) (TZS 100,000 / 30 days)". Both the landlord's contract page and the renter's show the per-period figure with the basis underneath.
1b. **Templates are bilingual.** A template carries an English body and an optional Swahili one (`body_html_sw`), edited on EN/SW tabs with a per-language preview. A new contract is issued in the **renter's own language** unless the landlord picks otherwise on the "Document language" control, and the choice is frozen on the contract; an org that has written no Swahili body keeps issuing the English document rather than a blank one. Contracts issued before Part 2 read English.
2. Editing a template never changes active contracts (snapshot rule) — banner states this, and contracts signed before this change keep the wording they were signed with.
3. Contract lifecycle: `draft → pending_signature → active → expiring → ended | terminated`.
4. ~30 days before end date contract flags **expiring**; landlord prompted to renew (new contract, current price, renter confirms via SMS link) or let lapse.
4a. **Changing a running contract (Phase 31).** Contract page → **Change contract** (or **Renew**). The drafter (manager or owner) picks the effective date (the first day of one of the contract's periods, or its end date to renew), changes rent, cadence, due day, term or template, optionally **edits the wording** of this contract only, and writes a note to the renter in **Swahili and English** (both required). A manager **submits for approval**; the owners see "Contract change awaiting approval" in the bell and the contract list's *Awaiting approval* filter. An owner reviews old vs new (fields and a wording diff, both notes) and **approves**, **returns** with a reason (back to the drafter to edit and resubmit) or **rejects**. An owner who drafted it may approve it at once. Nothing reaches the renter before approval.
4b. On approval the renter gets an SMS in their language — the org, the unit, the date the change applies from, the owner's note and the link. The contract page shows the new document with the note on top; the renter **signs** (then the landlord activates and the new contract takes over, as in 22.4) or **declines** with a reason. A decline ends the amendment, the running contract carries on unchanged, and the org sees "Renter declined the contract change" in the bell with the reason.
5. Early termination: landlord terminates with reason; remaining schedules cancelled/waived; unit → vacant; SMS to renter.
6. **Arrears after move-out (Phase 21).** Periods the renter lived through stand after a termination or a natural end. The landlord keeps collecting them: Record payment and the renter's proof of payment both still work on a terminated or ended contract, for what is owed (nothing rolls into the future). Payments → **Former tenants** lists every closed tenancy that still owes, newest first, with the total. When the money will never come, the **owner** writes the rest off with a reason: the rows become `written_off`, leave `outstanding` and the Former tenants list, stay in `expected` (so the collection rate shows the loss), and can be **undone** if the former tenant turns up.

---

## 7. Payment collection — MVP offline flow

**Renter:**
1. Home opens on a **next-due hero block**: the due date and amount in full size, a countdown chip in the stamp colours — "Due in 5 days" / "Due today" / "3 days overdue", or "Awaiting confirmation" while a proof is pending — and two full-width buttons, **How to pay** and **Send proof**. The same chip repeats in the contract header and on every row of the Payments tab, which also keeps the payment history.
2. **How to pay** jumps to the "How to pay" card **pinned at the top of the Payments tab**: bank name, account name, account number with a copy button, the landlord's instructions text and the reference hint "use your unit name", plus the mobile-money block when the org has one. It collapses only after the first view, and an org with no details saved shows "Ask your landlord for payment details" rather than nothing.
3. Renter pays outside the system (cash / bank transfer / mobile money) to the org's **bank collection account**.
4. **Send proof** (home hero, Payments tab, or the contract page) opens a sheet pre-filled with the next-due amount and schedule: amount, date, method, reference, note and a photo/PDF picker (camera on mobile), with the payment instructions repeated inside the sheet. On submit the schedule row shows an "Awaiting confirmation" pencil chip. No SMS goes out — the landlord's badge is the signal. The renter can **withdraw** a proof while it is still unanswered, and reads the whole history under Payments → History.

**Landlord:**
1. Money arrives → landlord opens renter or schedule → **Record payment**: amount, method, reference no., date, note.
1a. **Or a proof arrives.** Payments gains a **Proofs** tab — first tab, with a badge in the nav and the bottom bar, while anything is unanswered — listing claims **oldest first**: renter, unit, claimed amount against what the schedule expects, date, thumbnail. The detail sheet shows the full image or PDF beside the two figures, and **Accept** opens the ordinary Record-payment sheet pre-filled from the proof, so the overpay confirm and the reversal path are unchanged; accepting links the payment to the proof and queues the thank-you SMS. **Reject** takes a reason, which is texted to the renter (`proof_rejected`) and shown in their app above a "Send again" button. The renter's detail page and the contract page list their proofs.
2. Full amount → schedule `paid`; underpayment → `partial` (remainder tracked); overpayment → applied to next schedule (confirm prompt).
3. System sends **thank-you SMS** with next due date.
4. Mistake → **reverse payment** (audited), schedule reverts. An accepted proof stays accepted — the claim was answered; the reversal is a fact about the payment.
4a. **A tenancy older than TMS.** A landlord-created contract may start up to **10 years** in the past (a renter's own application still may not), so the real move-in date generates the real rent book and the past periods come out `overdue` — the truth until they are settled. **Backfill history** on the contract page (also offered from the Overdue list) closes every unpaid row up to a chosen date in one call: *Paid* records one payment per row at its own due date, stamped `source=backfill` and chipped "backfilled" beside "imported" on both landlord and renter screens, or *Waived* marks the rows waived with a note. Rows already paid or partial are skipped and reported back. No text per row — one `backfill_done` SMS, "your rent book now shows history up to {date}". Reports count that money in the period its `paid_at` falls in, not the day it was typed. **Offline contract (Phase 30).** A landlord who onboarded a long-standing renter on a fresh contract ticks "Record an offline contract" in the Backfill sheet, enters the move-in date from the paper contract and, if different, the rent per period back then. The preview (a server dry run) shows "An offline contract with N periods will be recorded · M periods · TZS X"; confirming records an ended contract for the same renter — labelled "Offline contract", never signed in TMS, no document — settles its periods up to the chosen date, and leaves the running contract as it was (unless `until` reaches it, in which case its periods are settled too). The Backfills list shows "created offline contract {from} – {to}, N periods"; Undo removes the contract and its periods again. The Backfill CSV takes `from` and `period_amount` for the same thing and needs no running contract at all: the renter must already be in TMS, the unit is found by its code. **One line per receipt (Phase 32).** Give `amount` (the total on the receipt) instead of `period_amount` and each line becomes its own offline contract with one period and one payment on its date — a renter with two receipts gets two lines and two contracts. A stretch that runs into a contract already in TMS (or into the next receipt's stretch) ends the day before that one starts.
5. Dashboard card **"Due in the next 7 days"** (count + total, top 5 rows) links to Payments → **Due soon**, a 14-day window by default with a 7 / 14 / 30 picker. The Renters list carries a sortable **Next due** column tinted when overdue, the renter's header shows Next due / Overdue above the tabs, and occupied cards on the units board carry a "Due 3 Oct" chip.

**System:**
- Due date passes unpaid → schedule `overdue`; overdue SMS daily until resolved.
- All movements appear in reports and payment history immediately.

**Post-MVP:** step 2 replaced by **Scan to Pay** — renter scans pay-QR, gateway intent, webhook auto-confirms. Same schedule/payment records; only the entry method changes.

---

## 8. Notifications timeline (system)

For each payment schedule:

```
due-7d ──────── due date ─────── overdue ───────────── paid
   │               │                │                    │
reminder SMS   reminder SMS    daily overdue SMS    thank-you SMS
"due in 1wk"   "due today"     until resolved       + next due date
```

- Every message goes out in the **recipient's own language** (their saved preference; the org's default language only covers renters who never chose one). The language used is recorded in the log.
- All sends deduped per schedule per day; logged in notification log (landlord can view delivery status).
- **Proof of payment (flow 7):** submitting one sends **nothing** — the landlord's badge is the signal, the same rule as signing. Rejecting one sends `proof_rejected` with the landlord's reason; accepting one sends the existing `thank_you` with the next due date. The three reminder kinds carry `{{pay_link}}`, the deep link to the renter's payments screen where the instructions and "Send proof" sit.

| Kind | Trigger | Reaches |
|------|---------|---------|
| `proof_rejected` | landlord rejects a proof, with a reason | the submitting renter, in their own language |

- Landlord can also send **custom bulk SMS** to all/selected renters (e.g. water outage notice) — permission-gated, audited. The compose screen has **SW and EN tabs** with a recipient count per language; each renter receives the body in their language, falling back to the other one if only one was written.
- **SMS credits:** the Notifications header shows the org's prepaid balance, a low-balance warning under the watermark, and "N messages held". A send with no credit left is parked as **held_no_credit** (not failed) and goes out in order once the platform tops the org up; a bulk send that would exceed the balance is refused up front with the shortfall.

---

## 9. Reports & dashboard (landlord)

Dashboard (layout per org's saved **dashboard preferences**):

- **Total assets:** properties count, units count, occupancy %.
- **Total renters** (active contracts).
- **Payment status per renter:** Paid / Pending (within cycle) / Overdue — filterable table, CSV export.
- **Collections:** expected vs collected this period, trend chart.
- **Revenue this period** (sparkline + Δ%), **Expenses**, **Net income** cards.
- Drill-down: renter → contract → schedules → payment history (full retained record).

**Period picker.** Every report and dashboard card is driven by one shared picker: **month / quarter / 6 months / year / custom range**, with prev-next navigation. The window and the equivalent **previous window** are resolved server-side, so every figure carries a "vs previous period" change.

**Reports tabs:**
- **Overview** — tiles for the selected window with Δ vs the previous one.
- **Revenue** — collected vs expected as lines/area, expenses as bars, net as a line; hover tooltips; per-property toggle.
- **Expenses** — stacked bars by category plus the ledger table.
- **Payment status** and **Collections** as before, now windowed by the same picker.
- **Occupancy** over time; all series bucketed by day/week/month according to the window length (auto: day ≤ 62 days, week ≤ 26 weeks, else month).

**What the numbers mean.** The window is resolved server-side on the Dar es Salaam wall clock and echoed back half-open (`from` inclusive, `to` exclusive) with the equivalent `previous` window, so every "vs previous period" figure on the page comes from one place. A custom range opened on the 12th charts from the 12th — buckets are not snapped back to the 1st. Occupancy is measured on the **last day inside** each bucket and reported as a **percentage (0–100)**; the collection rate is a **fraction (0–1)**. A Δ against a previous period of zero shows as "—", not "+100%".

---

## 10. Audit trail (landlord + admin)

- Org-scoped audit page: filter by actor, entity, date range.
- Every action recorded: logins, KYC edits, price changes, contract events, payments recorded/reversed, SMS sends, settings changes.
- Append-only; platform admin sees cross-org for support/disputes.

---

## 11. Platform admin

1. Admin logs into `apps/admin`.
2. Org list: activate / suspend orgs, view platform metrics (orgs, renters, SMS volume, failed sends).
3. Cross-org audit search for support cases.
4. **User directory**: search any user by phone, e-mail or name → detail page (identity, orgs, tenancies, payments,
   activity). Suspend / activate with a reason (sessions revoked), fix a mistyped name with a reason, and reveal a
   renter's NIDA for a support case — every one of those audited, the reveal visible to the renter.

---

## 12. Expenses (landlord)

1. **Expenses** nav item → ledger for the selected period (shared period picker), filterable by property, unit and category, totals with the accountant's double rule, CSV export.
2. **Record expense** sheet: property (optional unit), category, amount, date incurred, vendor, reference, note, optional **receipt** upload (JPEG/PNG/PDF, ≤ 5 MiB).
3. Expense detail: receipt viewer, edit (audited before/after), and **void with a reason** — the row stays and is marked voided, exactly like reversing a payment; nothing is deleted and nothing is restored.
4. Each property page carries an **Expenses tab** for that property alone; the all-properties **summary** groups totals by property or by category with a grand total.
5. Settings → **Expense categories**: rename, reorder, deactivate; seeded with Repairs & maintenance, Utilities, Security, Cleaning, Taxes & levies, Insurance, Management fees, Other.
6. Expenses feed the Reports revenue/net figures (flow 9) by `incurred_on`; voided rows are excluded everywhere.

**Edge cases:** a date in the future beyond tomorrow, a unit that is not in the chosen property, an inactive category, or an amount outside bounds → validation error (a mismatched unit/category is a 422 naming the field, a cross-org id a 404); a voided expense cannot be edited or voided twice, and its receipt upload is refused too. A category that has expenses filed under it cannot be deleted — deactivate it. The summary keeps a row per property (or per active category) even at zero spend, so the chart's bars and colours are stable month to month.

---

## 13. Platform admin: templates & SMS credits

**SMS credits (per org):**
1. Admin opens **Orgs → org detail → SMS**: balance, low watermark, credits used in 30 days, messages currently held, and the full credit ledger.
2. **Top up** (credits + note), **adjust** (signed delta + note) or change the **watermark** — every action audited platform-side and in the org's own log, where the landlord reads it as "credits added by platform".
3. Topping up releases **every** one of the org's `held_no_credit` messages, oldest first, and the response says how many (`released`). The debit happens at send time, so a release the balance cannot cover costs nothing — the worker simply holds the surplus again, in the same order.
4. Admin dashboard metric: credits consumed today, and orgs sitting under their watermark.

**Message templates:**
1. **Templates** nav item lists every notification kind with its SW and EN bodies side by side, the variables it may use as chips, its version and last editor.
2. Editing validates the placeholders against that kind's allowed variables, warns beyond three SMS segments, saves a new version and audits the change; **Preview** renders it with sample values.
3. **Version history** shows previous bodies with a one-click **revert**.
4. **Lock** a kind so landlords cannot override it — a landlord editing a locked kind sees read-only wording (with a "Show platform wording" toggle) and an override attempt is refused (`template_locked`); *clearing* an override is always allowed, since that moves the org back to the platform's wording. **`otp` ships locked and is not org-overridable at all**, so it does not appear on the landlord's notification settings screen in any form — the admin sees it locked in the catalogue.
5. Resolution order at send time: org override → platform template → built-in code default.

---

## 14. Import previous records (landlord)

A landlord arriving with a spreadsheet of what already exists — rooms, renters, last year's rent — loads it once instead of typing it in.

1. **Settings → Import data**. Pick the kind: **units**, **renters** or **payments**. The page lists that kind's columns and what each one must contain, and offers **Download template** — a CSV with the fixed English machine headers and one example line. The headers are never the SW/EN screen labels, so a Swahili session and an English one upload the same file.
2. **Drop the file** (≤ 2 MiB, ≤ 5 000 rows; UTF-8 with or without a BOM, comma- or semicolon-separated — the delimiter is sniffed). → **Preview**: every line with its status, the errors named inline on the line they belong to, and what each ok line resolved to (property, unit, renter). Nothing has been written.
3. **Commit N rows**, or **Skip M rows with errors and commit** when some lines are bad. The commit runs in **one transaction** — a landlord never ends up with half a spreadsheet — and every row goes through the same validation as the manual screens.
4. What each kind creates: `units` → properties (created when missing, flagged in the preview) and units with their price; `renters` → renters pre-registered by phone and, where a unit is named, a contract at **`pending_signature`** — an import **never activates a contract**, the renter still signs (or the landlord countersigns on the flow 3.6 path); `payments` → payments allocated against the resolved contract in date order, which may belong to a finished tenancy because history usually does. Imported payments carry an "imported" pencil chip in every ledger.
5. **History** lists every batch with its counts and an **Undo** for 24 hours: payments reversed with the reason "import undone", units and renters the batch created removed when nothing has touched them since.

**Edge cases:** a payment row that exceeds the contract's remaining balance is an **error in the preview** — the landlord fixes the sheet or the contract dates rather than discovering it at commit; a cell beginning `=`, `+`, `-` or `@` is stored exactly as typed and neutralised on every export; 20 previews per hour; a batch older than 24 hours can no longer be undone, and a committed batch cannot be committed twice. On mobile the preview table scrolls horizontally.

---

## 15. Client-requirement traceability

| Client requirement | Covered by |
|---|---|
| Register tenants/users + KYC (names, NIDA, next of kin, contact, email) | Flow 2 |
| Payment duration 1/3/6/12 months (recommended) + landlord custom periods in days (7, 21, 45…) | Flow 1 step 3, Flow 2 step 5, Flow 5 |
| Select start/end dates | Flow 2 step 5 |
| Choose payment method (Scan to Pay) | Flow 7 (offline MVP; gateway post-MVP) |
| Make payment | Flow 7 |
| Reminder 1wk / due day / daily overdue / thank-you + next due | Flow 8 |
| Reports: assets, tenant count, per-tenant status, history | Flow 9 |
| Audit trail for all actions | Flow 10 |
| Payments to bank collection account | Flow 7 |
| Lavatory service (Haja Ndogo/Kubwa/Kuoga, 30/70 commission) | **Out of scope** (confirmed) |
| Landlord customization: names, pricing, terms, due dates, theme, logo, dashboard | Flows 1, 4, 5, 6, 9 |
| Bulk SMS via Beem | Flow 8 |
| QR-first tenant access | Flow 2 |

Part 2 requests (5 Sep 2026, numbering per [PLAN2.md](PLAN2.md) scope table):

| # | Client requirement | Covered by |
|---|---|---|
| 1 | Time-series graphs + trends for revenue | Flow 9 (Revenue tab, dashboard cards) |
| 2 | Expense tracking and logging | Flow 12 |
| 3 | Reports/trends with cadence: month, quarter, 6 months, annual, custom | Flow 9 (period picker) |
| 5 | Landlord app mobile-friendly | Flows 4–12 on mobile (Phase 9 shell, Phase 15 full pass) |
| 6 | Landlord branding: choose app theme — presets + advanced override | Flow 1 step 3.1 |
| 7 | Landlord left nav reloads on page change | Flows 3–12 (nav is layout-level; state survives navigation) |
| 8 | "Recommended" shows on every payment period — align semantics | Flow 1 step 3.3, Flow 2 step 5, Flow 5 step 2a |
| 9 | Renter contract page overflows on mobile | Flow 2 step 7b, Flow 3 step 6 (document wraps at 320–414 px) |
| 10 | Swahili/English per user → screens + SMS/bulk SMS | Flow 1 step 3.7, Flow 2 steps 2–3, Flow 8 |
| 11 | Admin sets an SMS balance per landlord | Flow 13, Flow 8 (landlord's view of balance and held messages) |
| 12 | All message templates (EN + SW) configurable on the admin page | Flow 13 |
| 13 | Renter sends proof of payment; landlord accepts or rejects it | Flow 7 (renter steps 4, landlord step 1a), Flow 8 |
| 14 | Import previous records (units, renters, payments) from a spreadsheet | Flow 14 |
| 15 | Next payment due visible to both sides | Flow 7 (renter step 1, landlord step 5) |
| 16 | Payment instructions easy to find | Flow 7 (renter step 2), Flow 1 step 4a |
