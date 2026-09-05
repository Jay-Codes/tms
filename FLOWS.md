# FLOWS.md — TMS User Flows

Companion to [SPEC.md](SPEC.md). Actors: **Renter** (`apps/enduser`), **Landlord user** (`apps/tenant`), **Platform admin** (`apps/admin`), **System** (scheduler/jobs).

---

## 1. Landlord onboarding

1. Landlord opens signup link → registers org: business name, owner name, email, phone, password.
2. Email verification link → verified.
3. Guided setup wizard:
   1. **Branding** — display name (e.g. "JJnE Rentals"), logo upload, optional letterhead upload + document footer text (used on contract documents), and the **app theme**: pick one of the eight presets (Ledger, Night ledger, Warm paper, Cool slate, Forest, Ocean, High-contrast, Minimal white) from a gallery with a live mini-ledger preview, or open **Advanced** to set individual tokens (paper, surface, ink, ink-muted, rule, primary, accent) and the font. Contrast badges warn live and a body-text pair below 4.5:1 is refused on save. The chosen theme applies to both the landlord app and the renter-facing pages; the admin app is never themed.
   2. **First property** — name (custom, e.g. "Mbezi Beach Block A"), location.
   3. **Payment periods** — list pre-seeded with four presets (Monthly 30d, Quarterly 90d, Half-year 180d, Yearly 365d). **Monthly** carries the single "Recommended" badge; "Set as recommended" moves it to any other period, and only one period can hold it. Landlord keeps/removes any, and adds custom periods as a label + number of days (e.g. "Weekly" 7d, "3 weeks" 21d, "45 days") — no limit on count or value.
   4. **Units** — add units with custom names ("Room 1", "House B"), set price (amount per N days, default 30) and optionally restrict which payment periods this unit offers.
   5. **Contract template** — start from default terms, edit; set default due day and grace days.
   6. **Notification settings** — confirm reminder timings, sender name, and the **default language for renters without a preference** (SW/EN) — each renter's own choice wins over it.
   7. **Language preference** — the owner's own screen language (Kiswahili / English), prefilled from the signup toggle and changeable later in Settings → Preferences. It also decides the language of anything the platform sends to them.
4. Wizard ends on dashboard; empty-state cards prompt "Print QR codes" and "Invite staff".
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

**Edge cases:** QR of occupied unit → show "unit occupied — contact landlord" unless landlord enabled waitlist; wrong unit scanned → renter cancels request; OTP retries rate-limited with resend cooldown.

---

## 3. Link approval & contract activation (landlord)

1. Dashboard badge: pending link requests.
2. Open request → renter KYC details, chosen duration, start date.
3. Approve → contract created from template (terms + price **snapshotted**, hash computed), status `pending_signature`, renter SMS'd to sign.
4. Reject (with reason) → renter notified by SMS.
5. Renter signs (OTP + optional drawn signature) → landlord sees "Ready to countersign" → **Activate** records the landlord signature, generates payment schedules for the whole span, unit → occupied.
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
2. Editing a template never changes active contracts (snapshot rule) — banner states this, and contracts signed before this change keep the wording they were signed with.
3. Contract lifecycle: `draft → pending_signature → active → expiring → ended | terminated`.
4. ~30 days before end date contract flags **expiring**; landlord prompted to renew (new contract, current price, renter confirms via SMS link) or let lapse.
5. Early termination: landlord terminates with reason; remaining schedules cancelled/waived; unit → vacant; SMS to renter.

---

## 7. Payment collection — MVP offline flow

**Renter:**
1. Dashboard shows: **next payment due** (date + amount), status chip (paid / pending / overdue), payment history.
2. Renter pays outside the system (cash / bank transfer / mobile money) to the org's **bank collection account** shown on the payment screen with reference instructions.

**Landlord:**
1. Money arrives → landlord opens renter or schedule → **Record payment**: amount, method, reference no., date, note.
2. Full amount → schedule `paid`; underpayment → `partial` (remainder tracked); overpayment → applied to next schedule (confirm prompt).
3. System sends **thank-you SMS** with next due date.
4. Mistake → **reverse payment** (audited), schedule reverts.

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
- Occupancy over time; all series bucketed by day/week/month according to the window length.

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

---

## 12. Expenses (landlord)

1. **Expenses** nav item → ledger for the selected period (shared period picker), filterable by property, unit and category, totals with the accountant's double rule, CSV export.
2. **Record expense** sheet: property (optional unit), category, amount, date incurred, vendor, reference, note, optional **receipt** upload (JPEG/PNG/PDF, ≤ 5 MiB).
3. Expense detail: receipt viewer, edit (audited before/after), and **void with a reason** — the row stays and is marked voided, exactly like reversing a payment; nothing is deleted and nothing is restored.
4. Each property page carries an **Expenses tab** for that property alone; the all-properties **summary** groups totals by property or by category with a grand total.
5. Settings → **Expense categories**: rename, reorder, deactivate; seeded with Repairs & maintenance, Utilities, Security, Cleaning, Taxes & levies, Insurance, Management fees, Other.
6. Expenses feed the Reports revenue/net figures (flow 9) by `incurred_on`; voided rows are excluded everywhere.

**Edge cases:** a date in the future beyond tomorrow, a unit that is not in the chosen property, or an amount outside bounds → validation error; a voided expense cannot be edited or voided twice.

---

## 13. Platform admin: templates & SMS credits

**SMS credits (per org):**
1. Admin opens **Orgs → org detail → SMS**: balance, low watermark, credits used in 30 days, messages currently held, and the full credit ledger.
2. **Top up** (credits + note), **adjust** (signed delta + note) or change the **watermark** — every action audited platform-side and in the org's own log, where the landlord reads it as "credits added by platform".
3. Topping up releases the org's `held_no_credit` messages in queue order.
4. Admin dashboard metric: credits consumed today, and orgs sitting under their watermark.

**Message templates:**
1. **Templates** nav item lists every notification kind with its SW and EN bodies side by side, the variables it may use as chips, its version and last editor.
2. Editing validates the placeholders against that kind's allowed variables, warns beyond three SMS segments, saves a new version and audits the change; **Preview** renders it with sample values.
3. **Version history** shows previous bodies with a one-click **revert**.
4. **Lock** a kind so landlords cannot override it — `otp` ships locked; a landlord editing a locked kind sees read-only wording and an override attempt is refused (`template_locked`).
5. Resolution order at send time: org override → platform template → built-in code default.

---

## 14. Client-requirement traceability

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
