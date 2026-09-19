# UAT.md — user acceptance test script

Step-by-step acceptance run against the seeded **JJnE Rentals** demo data, in
two parts:

- **Part 1** — every flow in [FLOWS.md](../FLOWS.md) 1–11 (the MVP, Phases 0–8).
- **[Part 2 — UAT 2](#part-2--uat-2)** — the Part 2 work (PLAN2 Phases 9–15):
  mobile shell and nav, recommended period, rent per payment period, expenses,
  reports v2, theming, language, SMS credits, platform templates.

`{BASE}` is the preview origin: the ngrok URL printed by `make url`, or
`http://localhost:8080` when running locally. Every path below is
`{BASE}/tenant/…`, `{BASE}/enduser/…` or `{BASE}/admin/…`.

---

## 0. Before you start

```sh
make up          # postgres, redis, minio
make migrate     # schema up to date
make api         # Go API on :8081
make preview     # three apps + proxy on :8080 + ngrok, prints the public URL
make seed-demo   # JJnE demo data (idempotent — safe to re-run)
```

`make seed-demo` prints the unit codes at the end; they are reproduced in the
table below. It is idempotent and never touches Asha Mwakalinga's hand-made
tenancy.

### Accounts

| Role | App | Sign in with |
|---|---|---|
| Landlord (owner) | `{BASE}/tenant/login` | `demo@jjne.test` / `password123` |
| Renter — overdue | `{BASE}/enduser/login` | phone `+255766000001`, PIN `1234` (Neema Kileo) |
| Renter — paid | `{BASE}/enduser/login` | phone `+255766000002`, PIN `1234` (Baraka Mushi) |
| Renter — new tenancy | `{BASE}/enduser/login` | phone `+255766000003`, PIN `1234` (Upendo Sanga) |
| Renter — unsigned contract | `{BASE}/enduser/login` | phone `+255766000004`, PIN `1234` (Hamisi Ngassa) |
| Renter — pending application | `{BASE}/enduser/login` | phone `+255766000005`, PIN `1234` (Cecilia Mrema) |
| Platform admin | `{BASE}/admin/login` | `admin@tms.local` / `admin12345` |
| Load-test landlord | `{BASE}/tenant/login` | `load@tms.local` / `password123` (50 units — for volume checks only) |

`demo@jjne.test` is an extra owner account the seeder adds so UAT has a known
password; the org's original owner (`owner@jjne.test`) is untouched.

### Reading OTPs and SMS in dev

How you read an OTP depends on whether real Beem credentials are configured.

**Log provider** (`BEEM_API_KEY` blank in `.env`) — every SMS, OTP codes
included, is written to the API log instead of being sent:

```sh
make api-log                      # last 100 lines
tail -f .dev/api.log | grep sms_  # follow, OTPs only
```

Each send appears as one line:

```
level=INFO msg="sms (dev log provider)" sms_to=+255766000001 sms_sender=JJNE sms_body="Your TMS code is 483920..."
```

Take the code out of `sms_body`.

**Real Beem** (credentials set) — **nothing is logged**; the code has to arrive
on the handset, and `BEEM_SENDER_ID` must be a sender name Beem has **approved
for the account** or every send fails with `API_INVALID_PARAMETER: Invalid
Sender ID`. To go back to reading codes from the log, blank `BEEM_API_KEY` in
`.env` and run `make api-restart`.

Either way, OTP sends are rate-limited (3 per 10 minutes per phone), so do not
spam "resend".

### Demo unit codes

Scan URL is `{BASE}/enduser/u/{code}`.

| Property | Unit | Code | State |
|---|---|---|---|
| Mbezi Beach Block A | Room 4 | `RF6V17H895` | active tenancy, **overdue** schedule (Neema) |
| Mbezi Beach Block A | Room 5 | `EHHXDD787P` | active tenancy, first schedule **paid** (Baraka) |
| Kigamboni Court | Room 4 | `K4TNV6P65Q` | active tenancy starting today (Upendo) |
| Kigamboni Court | Shop B | `QXJFXHPWBM` | contract **pending signature** (Hamisi) |
| Mbezi Beach Block A | Room 1 | `PYJW8DCF69` | vacant, one **pending link request** (Cecilia) |
| Kigamboni Court | Shop A | `W9F0ZX74GT` | vacant — use this one for a fresh scan-and-register |
| Kigamboni Court | Room 3 | `WK2Q024TZV` | vacant |
| Kigamboni Court | Room 1 | `VBEHASWR1D` | vacant |

> Codes are regenerated only when a unit is created. If `make seed-demo` prints
> different codes, use the printed ones.

---

## Flow 1 — Landlord onboarding

Run this against a **new** org, not JJnE (JJnE is already set up). Sign out first.

- [ ] **1.1** `{BASE}/tenant/signup` — register "UAT Test Rentals", owner name, a
      fresh email, phone, password. → account created, you land signed in.
- [ ] **1.2** `make api-log` — the verification email link is logged. Open it, or
      `{BASE}/tenant/verify?token=…`. → email shows as verified.
- [ ] **1.3** `{BASE}/tenant/setup` — the wizard runs branding → property →
      payment periods → units → template → notifications. → each step saves and
      the next one opens.
- [ ] **1.4** Branding step: display name, theme colour, logo upload. → the app's
      colours change to match.
- [ ] **1.5** Payment-periods step: Monthly 30 / Quarterly 90 / Half-year 180 /
      Yearly 365 are pre-listed and badged **Recommended**. Add a custom
      "3 weeks" at 21 days. → it appears in the list.
- [ ] **1.6** Units step: add "Room 1" with a price per 30 days. → unit created
      with a unit code.
- [ ] **1.7** Finish the wizard. → you land on the dashboard; the empty-state
      cards prompt "Print QR codes" and "Invite staff".
- [ ] **1.8** `{BASE}/tenant/settings` → invite a staff member by email. →
      invitation recorded; the invite link is in `make api-log`.
- [ ] **1.9** Abandon and re-enter the wizard midway. → it resumes where you
      left it, and the dashboard shows a setup checklist.

Sign out and sign back in as `demo@jjne.test` for everything below.

## Flow 2 — Renter onboarding (QR-first)

- [ ] **2.1** In a private window (or on a phone), open
      `{BASE}/enduser/u/W9F0ZX74GT`. → JJnE branding, "Kigamboni Court / Shop A",
      the rent and a terms summary, with a "Register to connect" CTA.
- [ ] **2.2** Register with an unused phone, e.g. `+255766000010`. → "code sent".
- [ ] **2.3** `make api-log` → read the OTP from `sms_body`, enter it. → verified.
- [ ] **2.4** Set a PIN. → you are signed in as a renter.
- [ ] **2.5** KYC form at `{BASE}/enduser/profile`: full name, NIDA number, next
      of kin name + phone, contact number (prefilled), email. Optionally upload
      an ID photo. → saved; the NIDA is shown masked to its last 4 digits.
- [ ] **2.6** Choose a payment period from the offered list. → each option shows
      its **prorated** amount, recommended presets first.
- [ ] **2.7** Choose a tenancy length and start date. → the end date is derived
      and a schedule preview shows "N payments of X".
- [ ] **2.8** Review the terms and accept. → link request submitted; the status
      screen reads "Waiting for landlord approval".
- [ ] **2.9** Open an occupied unit's code, e.g. `{BASE}/enduser/u/RF6V17H895`. →
      "unit occupied — contact landlord".
- [ ] **2.10** Request an OTP four times in a row. → the fourth is refused with a
      resend cooldown, not an error page.

## Flow 3 — Link approval & contract activation

- [ ] **3.1** `{BASE}/tenant/link-requests` — as `demo@jjne.test`. → Cecilia
      Mrema's pending request for Mbezi Room 1 is listed, plus the one you just
      made in 2.8.
- [ ] **3.2** `{BASE}/tenant/link-requests/{id}` — open Cecilia's. → her KYC
      details, chosen duration and start date are shown.
- [ ] **3.3** Approve it. → a contract is created at `pending_signature` with the
      terms and price snapshotted; `make api-log` shows the "ready to sign" SMS.
- [ ] **3.4** Open your own request from 2.8 and **reject** it with a reason. →
      the renter is notified by SMS (check the log) and the request shows as
      rejected.
- [ ] **3.5** Sign in as Cecilia (`+255766000005` / `1234`),
      `{BASE}/enduser/contract/{id}` → read the document, then **Accept & sign**
      at `{BASE}/enduser/contract/{id}/sign`. → OTP sent; read it from the log.
- [ ] **3.6** Enter the code and optionally draw a signature. → signed; status
      reads "Waiting for landlord to countersign".
- [ ] **3.7** Back in the tenant app, `{BASE}/tenant/contracts/{id}` → the
      contract shows **Ready to countersign**. Click **Activate**. → schedules
      are generated for the whole term and the unit flips to `occupied`.
- [ ] **3.8** The renter gets the welcome SMS ("Your tenancy at … starts …") —
      check `make api-log`.
- [ ] **3.9** On the contract document: letterhead + logo, resolved terms,
      parties, schedule summary, signature block with names, timestamps, phone
      last-4 and drawn signatures, and the verification hash. Print it
      (Cmd-P / Save as PDF). → the print stylesheet renders cleanly on one
      document, no app chrome.
- [ ] **3.10** Hamisi Ngassa's contract (Kigamboni Shop B, `QXJFXHPWBM`) is
      already at `pending_signature`. → it appears in the contracts list awaiting
      the renter, and the unsigned-contract reminder is configured (Flow 8).

## Flow 4 — Vacancy management

- [ ] **4.1** `{BASE}/tenant/units` — filter by status. → **vacant / occupied /
      maintenance / unlisted** each return the right rows; Kigamboni Shop A,
      Room 1 and Room 3 are vacant.
- [ ] **4.2** `{BASE}/tenant/units/{id}` on an occupied unit → terminate its
      contract from `{BASE}/tenant/contracts/{id}`. → the unit flips to `vacant`
      and appears on the vacancy board.
- [ ] **4.3** Set a vacant unit to **maintenance**. → the override sticks and the
      unit leaves the vacancy list. Set it back to vacant. → control returns to
      the contract lifecycle.
- [ ] **4.4** Rename a unit. → the new name shows everywhere; the unit code is
      unchanged.
- [ ] **4.5** Open the vacated unit's scan URL. → it still resolves and offers
      registration.
- [ ] **4.6** `{BASE}/tenant/reports` → the occupancy tile and the vacant-unit
      list with days-vacant are populated.

## Flow 5 — Pricing management

- [ ] **5.1** `{BASE}/tenant/units/{id}` → **Prices**. → current price (amount per
      N days) plus the full history.
- [ ] **5.2** Add a new price with an `effective_from` in the future. → a banner
      states it applies to future contracts only; an active contract's schedule
      amounts do not change.
- [ ] **5.3** `{BASE}/tenant/settings/periods` → add a custom period ("45 days"),
      rename one, deactivate one, restore the recommended set. → all four work;
      changes affect future contracts only.
- [ ] **5.4** `{BASE}/tenant/units` → select several units → **bulk price
      update** by percentage, then by fixed amount. → both apply.
- [ ] **5.5** `{BASE}/tenant/audit` → filter to price changes. → who, when and
      old → new are recorded for each.

## Flow 6 — Contracts & terms management

- [ ] **6.1** `{BASE}/tenant/contracts/templates` → the default "Standard tenancy
      agreement" is listed and flagged default.
- [ ] **6.2** `{BASE}/tenant/contracts/templates/{id}` → edit in the rich-text
      editor (headings, lists, bold), insert a variable such as `{{renter_name}}`
      or `{{rent}}`. → the live preview renders it on the org's letterhead.
- [ ] **6.3** Save. → a banner states active contracts are unaffected; open
      Neema's contract and confirm its terms are unchanged.
- [ ] **6.4** Create a second template and make it default. → new contracts use
      it.
- [ ] **6.5** `{BASE}/tenant/contracts` → the status column shows the lifecycle:
      `pending_signature` (Hamisi), `active` (Neema, Baraka, Upendo, Asha).
- [ ] **6.6** Terminate a contract with a reason. → remaining schedules are
      cancelled/waived, the unit flips to vacant, the renter is SMS'd (check the
      log), and the original contract is kept, never edited.

## Flow 7 — Payment collection (offline MVP)

**Renter side**

- [ ] **7.1** Sign in as Neema (`+255766000001` / `1234`). `{BASE}/enduser` → the
      dashboard shows the next payment due (date + amount) with an **overdue**
      chip.
- [ ] **7.2** `{BASE}/enduser/payments` → payment history, plus JJnE's bank
      collection account (CRDB Bank, JJnE Rentals Ltd, 0150412345600) with
      reference instructions.
- [ ] **7.3** Sign in as Baraka (`+255766000002` / `1234`). → his first schedule
      shows **paid**, and the next due date is shown.

**Landlord side**

- [ ] **7.4** `{BASE}/tenant/payments` → **Record payment** against Neema's
      overdue schedule for the **full** amount (method, reference, date, note). →
      the schedule flips to `paid`.
- [ ] **7.5** Record a payment for **less** than the amount due. → the schedule
      becomes `partial` and the remainder is tracked.
- [ ] **7.6** Record a payment for **more** than the amount due. → a confirm
      prompt appears; confirm. → the excess rolls onto the next schedule.
- [ ] **7.7** After each recording, `make api-log` shows the thank-you SMS with
      the next due date.
- [ ] **7.8** `{BASE}/tenant/payments/{id}` → **Reverse** a payment with a
      reason. → the schedule reverts to its prior state (recomputing overdue),
      and `{BASE}/tenant/audit` carries the reversal.
- [ ] **7.9** Try to record a payment larger than everything the contract still
      owes. → refused with "more than the contract owes", and no payment row is
      written.
- [ ] **7.10** `{BASE}/tenant/settings/bank-account` → edit the collection
      account. → the renter payment screen shows the new details.

## Flow 8 — Notifications timeline

- [ ] **8.1** `{BASE}/tenant/settings/notifications` → sender name `JJNE`,
      language **English**, per-kind toggles for reminder-7d, reminder-due,
      overdue-daily, thank-you and unsigned-reminder. → all present and editable.
- [ ] **8.2** `{BASE}/tenant/notifications` → the delivery log lists seeded
      `reminder_due`, `overdue_daily` and `thank_you` rows with status `sent`.
- [ ] **8.3** Filter the log by kind and by status. → filters apply.
- [ ] **8.4** Retry a row from the log. → it is re-queued and re-sent (visible in
      `make api-log`).
- [ ] **8.5** Send a **custom bulk SMS** ("water outage on Friday") to all
      renters. → one row per recipient in the log; `make api-log` shows the sends;
      `{BASE}/tenant/audit` records who sent it.
- [ ] **8.6** Send the same broadcast twice in quick succession. → the second is
      deduped or rate-limited, not double-sent.
- [ ] **8.7** Switch the org's SMS language to Swahili and record a payment. →
      the thank-you body is in Swahili.

## Flow 9 — Reports & dashboard

- [ ] **9.1** `{BASE}/tenant` → total assets (properties, units, occupancy %),
      total renters on active contracts.
- [ ] **9.2** `{BASE}/tenant/reports` → the payment-status table per renter, with
      **Paid / Pending / Overdue** chips. Neema shows overdue (before you settle
      her in 7.4), Baraka paid, Upendo pending.
- [ ] **9.3** Filter that table by status. → filters apply.
- [ ] **9.4** Export it as CSV. → the file downloads and the rows match the
      screen.
- [ ] **9.5** Collections: expected vs collected for this period, with the trend
      chart. → figures are consistent with `{BASE}/tenant/payments`.
- [ ] **9.6** Drill down: renter → contract → schedules → payment history. →
      each level opens and the full retained record is there, reversals included.
- [ ] **9.7** `{BASE}/tenant/settings/branding` → change the dashboard
      preferences. → the dashboard layout follows them.

## Flow 10 — Audit trail

- [ ] **10.1** `{BASE}/tenant/audit` → entries for everything done above:
      logins, KYC edits, price changes, contract events, payments recorded and
      reversed, SMS sends, settings changes.
- [ ] **10.2** Filter by actor, by entity and by date range. → all three work.
- [ ] **10.3** Open one entry. → before/after values are shown.
- [ ] **10.4** Confirm the log is append-only: nothing in the UI edits or deletes
      an entry.

## Flow 11 — Platform admin

- [ ] **11.1** `{BASE}/admin/login` — `admin@tms.local` / `admin12345`. → signed
      in to the admin app, which uses the platform theme, not an org's.
- [ ] **11.2** `{BASE}/admin/orgs` → JJnE Rentals, Load Test Estates and the
      others are listed with their status.
- [ ] **11.3** `{BASE}/admin/orgs/{id}` → org detail: members, counts, settings
      summary.
- [ ] **11.4** **Suspend** the "UAT Test Rentals" org from Flow 1. → its landlord
      is refused with `org_suspended` on the next request. **Activate** it again.
      → access returns.
- [ ] **11.5** Platform metrics: orgs, renters, SMS volume, failed sends. →
      populated.
- [ ] **11.6** `{BASE}/admin/audit` → cross-org audit search returns entries from
      more than one org.
- [ ] **11.7** `{BASE}/admin/jobs` → last run times for the contract-lifecycle,
      overdue and notification jobs. Trigger the overdue job. → it runs and the
      timestamp updates.

## Cross-cutting checks

- [ ] **X.1 Isolation** — signed in as `demo@jjne.test`, request a Load Test
      Estates unit id directly, e.g. `{BASE}/tenant/units/{load-test-unit-id}`. →
      **404**, not 403 and not data.
- [ ] **X.2 Renter isolation** — signed in as Neema, open another renter's
      contract id. → 404.
- [ ] **X.3 Admin routes** — call an `/admin/*` route with a landlord session. →
      refused.
- [ ] **X.4 Volume** — sign in as `load@tms.local` / `password123`. →
      `{BASE}/tenant/units` (50 units), `{BASE}/tenant/contracts` (35) and
      `{BASE}/tenant/reports` all render without a perceptible wait. See
      [LOADTEST.md](LOADTEST.md) for the measured numbers.
- [ ] **X.5 PWA** — each app offers "Add to home screen" on a phone and its shell
      loads offline.
- [ ] **X.6 Mobile** — run Flow 2 end to end on a real phone over the ngrok URL,
      including the QR scan. → the QR resolves and every screen is usable at
      phone width.

---

# Part 2 — UAT 2

Acceptance run over the Part 2 work (PLAN2 Phases 9–15): the mobile landlord
pass, expenses, reports v2, theming, language, SMS credits and platform
templates. Same conventions as Part 1 — `{BASE}` is the preview origin, tick each
box, and for a failure record the item number, the URL, what you expected, what
happened, and the matching `make api-log` lines.

## P0. Preconditions

```sh
make up                # postgres, redis, minio
make migrate           # schema through 000016
make api               # Go API on :8081
make preview           # apps + proxy + ngrok; prints the public URL
make url               # copy it into .env as APP_BASE_URL and MINIO_PUBLIC_URL
make api-restart       # so QR targets and presigned links use the tunnel
make seed-demo         # JJnE demo data (idempotent)
```

- [ ] **P0.1** `make seed-demo` finished and printed the unit codes. It seeds the
      Part 2 fixtures too: expenses across both properties, a saved theme, and an
      edited platform template.
- [ ] **P0.2** **SMS**: decide which mode you are testing in and write it on the
      run sheet.
      - *Log provider* — `BEEM_API_KEY` blank in `.env`. Every message, OTP codes
        included, lands in `.dev/api.log`: `tail -f .dev/api.log | grep sms_`,
        code in `sms_body`. Nothing reaches a handset.
      - *Real Beem* — credentials set **and `BEEM_SENDER_ID` a sender name Beem
        has approved for the account**. Nothing is logged; the SMS has to arrive
        on the phone. An unapproved sender fails every send with
        `API_INVALID_PARAMETER: Invalid Sender ID` (and the credit is still spent
        on the attempt).
      Switching back is: blank `BEEM_API_KEY`, `make api-restart`.
- [ ] **P0.3** JJnE has SMS credit. `{BASE}/admin/orgs/{jjne-id}` → **SMS** tab →
      balance > 0. If it is 0, top it up now; every send below would otherwise be
      held.
- [ ] **P0.4** **Run the whole of section P1 on a real phone** over the ngrok URL,
      not a desktop window narrowed to 375 px. The rest may be run on a desktop
      unless an item says otherwise.
- [ ] **P0.5** Note the landlord's and the renter's current language before you
      start, and restore both at the end (section P6 changes them).

Evidence to capture throughout: a screenshot per checklist section, the
`{BASE}/tenant/audit` row for every mutation, and the `sms_` lines (or the
handset photo) for every send.

---

## P1. Navigation, no-remount, and the mobile shell (#5, #7)

### P1a. Nav does not remount (desktop, 1280 px)

- [ ] **P1.1** Sign in at `{BASE}/tenant/login` as `demo@jjne.test` /
      `password123`.
- [ ] **P1.2** Click through Dashboard → Properties → Units → Renters →
      Contracts → Payments → Expenses → Reports → Notifications → Audit →
      Settings. → the left nav **never flashes or refetches**: the pending-link
      badge, the org name and the theme stay painted through every navigation.
- [ ] **P1.3** With DevTools → Network open, navigate between two pages. → no
      repeat of the branding / badge calls the shell made on first load.
- [ ] **P1.4** Approve or reject a link request, then navigate away and back. →
      the badge count updates (on focus, not on route change) and is correct.
      *Evidence: screen recording of P1.2, network panel screenshot of P1.3.*

### P1b. Mobile shell — run at 375 px on the phone

For each screen: **no horizontal page scroll**, tap targets ≥ 44 px, sticky
action bars clear of the home indicator, and any wide table scrolling inside its
own container rather than pushing the page.

- [ ] **P1.5** **Bottom bar** — the five most-used sections are reachable from
      the fixed bottom bar; the current one is marked; it does not cover the last
      row of content or the sticky save bar.
- [ ] **P1.6** **Drawer** — the hamburger opens the full nav, closes on
      selection, closes on backdrop tap and on Back, and traps focus while open.
- [ ] **P1.7** **Sheets** — Record payment, Record expense, Void, Top-up-style
      confirm sheets open **full-screen** on mobile, scroll internally, and their
      primary button stays reachable with the keyboard open.
- [ ] **P1.8** **Tables** — Units, Renters, Contracts, Payments, Expenses, the
      Reports tables and the **Messages log** each scroll horizontally inside
      their own wrapper. (Phase 14 noted the Messages log table as missing its
      wrapper at 375 px — check it explicitly.)
- [ ] **P1.9** Screen-by-screen pass at 375 px: `/tenant` dashboard,
      `/tenant/properties` + a property, `/tenant/units` (card view) + a unit,
      `/tenant/link-requests` + one request, `/tenant/renters` + one renter,
      `/tenant/contracts` + one contract **document**, `/tenant/payments`,
      `/tenant/expenses` + one expense, `/tenant/reports` (every tab),
      `/tenant/notifications`, `/tenant/settings/*`,
      `/tenant/settings/branding`, `/tenant/audit`.
- [ ] **P1.10** Repeat P1.9 at **414 px** and **768 px**. → 768 px is the
      breakpoint: at and above it the left nav is back and the bottom bar is
      gone; nothing is stranded in between.
- [ ] **P1.11** Print the contract document from the phone/desktop
      (`/tenant/contracts/{id}` → Print). → the print sheet is **unaffected** by
      the mobile layout and by the org theme: light paper, full terms, signature
      block, no nav chrome.
- [ ] **P1.12** No console errors on any screen. In particular there should be
      **no hydration mismatch** logged from the branding style on `<html>`
      (open Phase 14 note).
- [ ] **P1.13** **PWA** — the tenant app offers "Add to home screen"; installed,
      it opens on the dashboard with the org theme and no browser chrome.
- [ ] **P1.14** Renter app re-check at 375 px in **both languages** — Swahili
      strings are longer: `/enduser`, `/enduser/payments`, `/enduser/profile`,
      `/enduser/contract/{id}`, `/enduser/u/{code}`. → nothing clips, wraps
      badly, or overflows.
      *Evidence: one screenshot per screen at 375 px, in both languages for the
      renter app.*

---

## P2. Recommended payment period (#8)

- [ ] **P2.1** `{BASE}/tenant/settings/periods` → **exactly one** period carries
      the "Recommended" badge (Monthly by default). The other seeded presets
      (Quarterly, Half-year, Yearly) are listed without one.
- [ ] **P2.2** "Set as recommended" on Quarterly. → the badge **moves**: Monthly
      loses it in the same action, and the list re-orders recommended-first.
- [ ] **P2.3** `{BASE}/tenant/audit` → a `payment_period.recommend` entry whose
      before/after name the period that lost the badge and the one that gained
      it.
- [ ] **P2.4** Open a vacant unit's QR landing on the phone
      (`{BASE}/enduser/u/W9F0ZX74GT`) and start "connect". → the period list
      shows Quarterly first and badged, and **only** Quarterly badged.
- [ ] **P2.5** Delete a seeded period, then "Restore defaults" on the periods
      screen. → the missing preset comes back **unbadged**; the recommended
      period is unchanged.
- [ ] **P2.6** Set the badge back to Monthly.
      *Evidence: periods screen before/after, renter connect screen, audit row.*

---

## P3. Rent per payment period in a new contract document

- [ ] **P3.1** Pick a vacant unit priced per 30 days (e.g. Kigamboni Court
      Room 3, `WK2Q024TZV`, at TZS 100,000 / 30 days). Create a tenancy on a
      **Quarterly (90 days)** payment period — through the renter connect flow,
      or landlord-side from the unit.
- [ ] **P3.2** Approve the request and open the contract document at
      `{BASE}/tenant/contracts/{id}`. → the terms read **"rent of TZS 300,000 per
      Quarterly (90 days) (TZS 100,000 / 30 days)"**: `{{rent}}` is the amount
      due each payment period and `{{rent_basis}}` keeps the unit price.
- [ ] **P3.3** The header "Rent" row shows the per-period figure with the basis
      underneath, and it **matches a full schedule row** on the contract's
      schedule list.
- [ ] **P3.4** Open an **older** contract signed before this change. → its
      wording is untouched (snapshot rule) and `/verify` still reports the
      contract as valid — the hash is unaffected.
- [ ] **P3.5** Renter side, **at 375 px**: sign in as that renter and open
      `{BASE}/enduser/contract/{id}`. → the parties table wraps instead of
      clipping: long names, the masked phone on its own line, the term dates not
      broken across the en-dash, no value cut off, no horizontal page scroll.
      Repeat at **320 px** and **414 px**.
      *Evidence: contract document screenshots (landlord + renter at 375 px),
      schedule list.*

---

## P4. Expenses (#2, flow 12)

- [ ] **P4.1** `{BASE}/tenant/expenses` → the ledger for the current period, with
      the period picker, property/category filters, a summary strip and totals
      under a double rule.
- [ ] **P4.2** **Record expense** — Mbezi Beach Block A, category *Repairs &
      maintenance*, an amount, today's date, a vendor and a note, with a **receipt**
      (JPEG or PNG, and repeat once with a PDF). → the row appears with a receipt
      chip; `{BASE}/tenant/expenses/{id}` shows the receipt in the viewer.
- [ ] **P4.3** Edit that expense (change the amount and the note). →
      `{BASE}/tenant/audit` carries an `expense.update` with before/after.
- [ ] **P4.4** Validation: a date more than a day in the future, an amount of 0,
      and a unit that belongs to a **different** property than the one chosen. →
      each is refused with the field named; nothing is written.
- [ ] **P4.5** **Property tab** — `{BASE}/tenant/properties/{mbezi-id}` →
      Expenses section shows that property's rows and its total only.
- [ ] **P4.6** **All-properties summary** — back on `/tenant/expenses`, switch
      the summary grouping between **property** and **category**. → every live
      property (and every active category) has a row even at zero spend, the
      groups sum to the grand total, and the Δ vs the previous period reads "—"
      rather than "+100%" when the previous period was empty.
- [ ] **P4.7** **Void** one expense with a reason. → the row stays, stamped
      VOIDED, **drops out of every total** including the reports, cannot be edited
      or voided again, and `expense.void` is audited. Nothing was restored or
      deleted.
- [ ] **P4.8** **CSV** export with a property filter applied. → the file
      downloads as `expenses-{from}-{to}.csv`, its rows match the screen, and a
      cell that starts `=`, `+`, `-` or `@` is prefixed with `'`.
- [ ] **P4.9** **Categories** — `{BASE}/tenant/settings/expense-categories`:
      rename one, reorder, deactivate one (it disappears from the record sheet's
      picker but existing rows keep it), and try to **delete** a category that has
      expenses. → refused, with "deactivate it instead".
- [ ] **P4.10** Cross-org: as `demo@jjne.test`, open a Load Test Estates expense
      id directly. → **404**.
      *Evidence: ledger with totals, receipt viewer, voided row, CSV file,
      summary in both groupings, audit rows.*

---

## P5. Reports v2 (#1, #3)

- [ ] **P5.1** `{BASE}/tenant/reports` → one period picker above six tabs
      (Overview, Revenue, Expenses, Occupancy, Payment status, Collections) and a
      property filter.
- [ ] **P5.2** **Cadence** — step the picker through **month**, **quarter**,
      **6 months**, **year** and a **custom** range, with prev/next navigation in
      each. → every tab reloads against the same window; the window shown matches
      the one you chose; a custom range starting mid-month charts **from that
      day**, not from the 1st.
- [ ] **P5.3** Custom range refusals: `from` after `to`, and a range longer than
      five years. → refused with a readable message, not a blank chart or a 500.
- [ ] **P5.4** **Overview** — tiles for the window (collected, expected,
      expenses, net, collection rate, occupancy), each with **Δ vs the previous
      period**. → the arrows and signs agree with the figures; a Δ against a zero
      previous period shows "—".
- [ ] **P5.5** **Revenue** tab — collected vs expected as lines/area, expenses as
      bars, net as a line, with hover tooltips and a legend. → the chart is
      readable, axes are labelled, and the totals under it reconcile with
      `{BASE}/tenant/payments` and `/tenant/expenses` for the same window.
- [ ] **P5.6** **By property** — switch the revenue breakdown to per-property. →
      one row per live property including those that earned nothing; the rows
      **add up to the grand total**.
- [ ] **P5.7** **Expenses** tab — stacked bars by category plus the table; the
      figures match the expenses ledger for the window, voided rows excluded.
- [ ] **P5.8** **Occupancy** tab — the series is measured at each bucket end and
      reads as a **percentage 0–100**; the current figure matches the dashboard's
      occupancy.
- [ ] **P5.9** **Payment status** and **Collections** still work under the shared
      picker; payment status is a statement about *now* and does not change when
      you move the window; the CSV export still downloads.
- [ ] **P5.10** **Property filter** — pick one property. → every tab narrows,
      including expenses, and the numbers are a strict subset of the all-property
      run.
- [ ] **P5.11** **Dashboard cards** — `{BASE}/tenant` shows "Revenue this period"
      (sparkline + Δ%), "Net income" and "Expenses"; Customize adds/removes them
      and the choice survives a reload.
- [ ] **P5.12** At 375 px: charts scale, legends wrap, tables scroll in their own
      wrapper, and the picker is usable with a thumb.
      *Evidence: one screenshot per tab at year cadence, one at 375 px, the
      reconciliation numbers written down.*

---

## P6. Theming v2 (#6)

- [ ] **P6.1** `{BASE}/tenant/settings/branding` → the preset gallery shows
      eight presets, each with a live mini-ledger preview.
- [ ] **P6.2** Apply **Night ledger** and save. → the tenant app repaints
      immediately: paper, surface, ink, rules, primary and accent all move; the
      **Paid / Overdue stamps take their dark step** and stay legible.
- [ ] **P6.3** **Advanced** — tweak the accent (and one other token). → contrast
      badges update live per swatch; Save is blocked while a body-text pair is
      below 4.5:1.
- [ ] **P6.4** **Contrast block** — deliberately set `ink` close to `paper` and
      save. → refused with the failing pairs and their ratios named; **nothing is
      written** (reload: the previous theme is still in force).
- [ ] **P6.5** Reset to the preset, then save a valid custom set. → the screen
      says the theme is the preset "edited", and the reload keeps it with no
      flash of the old colours.
- [ ] **P6.6** **Renter app follows** — on the phone, open
      `{BASE}/enduser/u/{code}` (before signing in) and then sign in as a JJnE
      renter. → both the public landing and the signed-in screens use the org's
      theme, and the browser UI colour follows the paper colour.
- [ ] **P6.7** **Admin app is never themed** — `{BASE}/admin` still uses the
      platform chrome.
- [ ] **P6.8** **Print stays light** — with Night ledger active, print a contract
      document from both apps. → the printed sheet is light paper with dark ink,
      not the dark theme.
- [ ] **P6.9** `{BASE}/tenant/audit` → a `branding.theme_update` entry with
      before/after.
- [ ] **P6.10** Restore JJnE to the theme `make seed-demo` left it on — **Cool
      slate** with the teal accent override — or re-run `make seed-demo` (it
      leaves an already-chosen theme alone, so reset the theme first if you want
      it reseeded).
      *Evidence: gallery, dark tenant screen, dark renter screen, the rejected
      save with its failure list, the printed sheet.*

---

## P7. Language (#10)

- [ ] **P7.1** **Pre-auth toggle** — on the phone open `{BASE}/enduser/u/{code}`
      signed out. → the page opens in the **org's** language and shows an SW/EN
      toggle. Switch it. → the page switches, and the choice survives a reload.
- [ ] **P7.2** **Carried into registration** — with the toggle on Kiswahili,
      register a new renter. → the registration screens are Swahili, the OTP SMS
      is Swahili, and the new account's saved language is Kiswahili.
- [ ] **P7.3** **Profile switch** — sign in as Asha (or the new renter),
      `{BASE}/enduser/profile` → switch to English. → the app is English
      immediately; reload keeps it; `{BASE}/tenant/audit` (the renter's org)
      shows `user.locale_update`.
- [ ] **P7.4** **SMS in the renter's language** — record a payment against that
      renter. → the thank-you message is in **their** language, not the org's or
      the landlord's. Repeat with a renter on the other language.
- [ ] **P7.5** **Bulk with two bodies** — `{BASE}/tenant/notifications` → compose
      → SW and EN tabs, each with its own body, a recipient count per language,
      a segment counter and a per-recipient preview. Send. → each renter gets the
      body for their own language; the log shows one row per recipient with the
      **language** it went out in, and the counts match the pre-send preview.
- [ ] **P7.6** **One body only** — send a broadcast with only the Swahili body
      filled. → everybody gets it, including the English-speaking renters (better
      than silence), and the log says so.
- [ ] **P7.7** **Landlord preferences** — `{BASE}/tenant/settings/preferences` →
      switch the landlord to Kiswahili. → the whole tenant app is Swahili: nav,
      settings, reports (cadence labels *Mwezi / Robo mwaka / Miezi 6 / Mwaka /
      Maalum*), tiles, sheets, empty states. No untranslated key anywhere
      (`make i18n-check` is the build-time half of this).
- [ ] **P7.8** Server data is **not** translated: unit and property names, custom
      period labels and rendered contract HTML read exactly as the landlord typed
      them in either language.
- [ ] **P7.9** **Org default** — `{BASE}/tenant/settings/notifications` → the
      language field is labelled as the default for renters *without* a
      preference. Change it, then send to a renter who has one. → their own
      language still wins.
- [ ] **P7.10** **Bilingual template + document language** —
      `{BASE}/tenant/contracts/templates/{id}` has EN and SW tabs with a
      per-language preview; issue a new contract with **Document language** set
      to Kiswahili. → the document renders in Swahili throughout (including the
      due-day and rent-basis phrases), the contract detail shows its language, and
      an org with **no** Swahili body issues the English document rather than a
      blank one.
- [ ] **P7.11** The admin app stays English regardless of anybody's locale.
- [ ] **P7.12** Restore the landlord's and the renter's original languages.
      *Evidence: renter app in both languages, tenant app in Swahili, bulk
      compose with both tabs, the log's language column, the Swahili contract.*

---

## P8. SMS credits (#11)

- [ ] **P8.1** **Admin view** — `{BASE}/admin/orgs/{jjne-id}` → **SMS** tab:
      balance, low watermark, credits used in 30 days, held count, and the
      ledger.
- [ ] **P8.2** **Watermark** — lower it, then raise it above the balance. → the
      landlord's `{BASE}/tenant/notifications` credits card and the dashboard
      banner switch to the low state at the boundary, and back.
- [ ] **P8.3** **Landlord card** — the Messages header shows balance, "N messages
      held" and the low-balance warning; it is **read-only** (no way for the
      landlord to buy credit in-app).
- [ ] **P8.4** **Adjust down** to a small balance (e.g. 5), then attempt a bulk
      send to every renter. → **refused before anything is queued**, with the
      shortfall: needed vs balance. Nothing appears in the log.
- [ ] **P8.5** Adjust to 0 and trigger a single send (record a payment). → the
      log row is **held**, not failed; retry on it is refused; the message body is
      intact.
- [ ] **P8.6** **Top up** from the admin app. → the response says how many held
      rows were released; the held rows go out **oldest first**; the landlord's
      held count drops to 0 and the balance falls by the segments actually spent.
- [ ] **P8.7** **Segments** — send one short message and one long enough to be
      two segments. → the ledger debits 1 and 2 credits respectively; a message
      with an emoji costs roughly double (UCS-2).
- [ ] **P8.8** **OTP is exempt** — with the balance at 0, request a login OTP. →
      it still sends, and no credit is debited.
- [ ] **P8.9** **Ledger is a record** — every movement appears with its delta,
      the balance after it, the reason and the admin who made it; a debit names
      the message it paid for. `{BASE}/tenant/audit` shows the top-up as "credits
      added by platform" on the landlord's own side too.
- [ ] **P8.10** **Adjust below zero** is refused (a prepaid balance has no
      overdraft), naming the current balance.
- [ ] **P8.11** Isolation: an admin route for org A's credits is unreachable from
      a landlord session (and org B's credits are unreachable from org A's).
- [ ] **P8.12** Leave JJnE with a working balance before moving on.
      *Evidence: SMS tab with ledger, the 409 shortfall message, held rows before
      and after top-up, the landlord card in both states.*

---

## P9. Platform templates (#12)

- [ ] **P9.1** `{BASE}/admin/templates` → every notification kind with its SW and
      EN bodies, variable chips, segment counts, version and last editor. `otp`
      is listed and shown **locked**.
- [ ] **P9.2** **Edit** the Swahili `reminder_due` body at
      `{BASE}/admin/templates/reminder_due`. → placeholders are validated against
      that kind's variables (an unknown one is refused, naming it), both languages
      are required, and a body past three segments saves with a **warning** rather
      than a refusal.
- [ ] **P9.3** **Preview** with sample values, in each language. → the rendered
      body, its segment count and its encoding (GSM vs UCS-2) are shown; typing an
      emoji flips it to UCS-2 and roughly doubles the segments **before** saving.
- [ ] **P9.4** **It takes effect** — trigger a due reminder (`{BASE}/admin/jobs`
      → notifications job, or wait for the sweep). → the sent message uses the new
      wording, in the recipient's language. (The rendered-template cache is
      invalidated on save, so no five-minute wait.)
- [ ] **P9.5** **Version history + revert** — the history lists the previous
      bodies, newest first. Revert to an earlier version. → the old wording comes
      back as a **new** version (the counter never goes backwards) and the
      response names the version it was restored from.
- [ ] **P9.6** **Lock** — lock `reminder_due`. → on the landlord side,
      `{BASE}/tenant/settings/notifications` shows that kind **read-only**, with a
      "Show platform wording" toggle, and saving an override for it is refused
      (`template_locked`). *Clearing* an existing override is still allowed.
- [ ] **P9.7** `otp` appears **nowhere** on the landlord's notification settings —
      not as an editor and not as a locked row. It is not an org's to override at
      all.
- [ ] **P9.8** Unlock `reminder_due` again. → the landlord can override it once
      more, and the override wins over the platform wording at send time (org
      override → platform template → built-in default).
- [ ] **P9.9** `{BASE}/admin/audit` → `platform_template.update`,
      `platform_template.lock` and `platform_template.revert` entries. These carry
      **no org** — the wording belongs to the platform.
      *Evidence: editor with both languages, preview with segment/encoding,
      version history, the landlord's read-only view, the refused override.*

---

## P10. Admin metrics & dashboard

- [ ] **P10.1** `{BASE}/admin` → the SMS block shows credits consumed today, orgs
      under their watermark, held total, and the existing sent/failed/queued
      figures.
- [ ] **P10.2** The numbers move consistently with what section P8 did (top-up,
      held, released).
- [ ] **P10.3** `{BASE}/admin/orgs` still lists every org with its status, and
      suspend/activate still works (Part 1 flow 11.4) with Part 2 data present.
- [ ] **P10.4** `{BASE}/admin/jobs` shows last-run times for the
      contract-lifecycle, overdue and notification jobs and can trigger them.
      *Evidence: admin dashboard screenshot.*

---

## P11. Part 2 cross-cutting checks

- [ ] **P11.1** **Isolation** — `make test-isolation` is green, and by hand: as
      `demo@jjne.test`, request a Load Test Estates expense, contract and unit id
      directly → **404** each time, never 403 and never data.
- [ ] **P11.2** **Performance** — `make loadtest` with the reports v2 routes
      included: **p95 < 300 ms** on the seeded org. Record the numbers in
      [LOADTEST.md](LOADTEST.md).
- [ ] **P11.3** **Build health** — `make build`, `make test`, `make lint`
      (including `i18n-check`) and `make test-race` all green.
- [ ] **P11.4** **Volume at phone width** — sign in as `load@tms.local` /
      `password123` on the phone and open Reports at a **year** cadence, plus the
      expenses ledger. → both render without a perceptible wait and without
      horizontal scroll.
- [ ] **P11.5** **Rate limits** — more than 30 receipt uploads in an hour is
      refused with a `Retry-After`, and so is a burst of admin template edits.
- [ ] **P11.6** **Audit completeness** — every mutation this run made has a row
      in `{BASE}/tenant/audit` or `{BASE}/admin/audit`: recommend, expense
      create/update/void/receipt, theme update, locale update, credit top-up and
      adjust, watermark, template update/lock/revert.

---

# Phase 16 — proofs, import, due visibility

Run after P0's preconditions, as `demo@jjne.test` (landlord) and Asha
(renter) on the phone. Planned scope: PLAN2 §16, FLOWS 7 and 14.

## P12. Proof of payment (#13, flow 7)

- [ ] **P12.1** As Asha, `{BASE}/enduser` → the home hero shows the next due
      date and amount with a countdown chip, above **How to pay** and **Send
      proof**.
- [ ] **P12.2** **Send proof** → the sheet opens pre-filled with the next-due
      amount and schedule, repeats the org's payment instructions, and takes a
      photo from the camera (repeat once with a PDF). Submit. → the schedule row
      shows an **"Awaiting confirmation"** pencil chip, and **no SMS** is sent
      (`make api-log` shows nothing for the submit).
- [ ] **P12.3** As the landlord, `{BASE}/tenant/payments` → a **Proofs** tab,
      first, with a badge in the nav and the bottom bar; the dashboard carries a
      `proofs` card. The claim lists renter, unit, claimed vs expected, date and
      a thumbnail, **oldest first**.
- [ ] **P12.4** Open it → the full image/PDF viewer with claimed beside what the
      schedule expects. **Accept** → the Record-payment sheet opens pre-filled;
      confirm. → the schedule flips **paid**, the proof reads accepted and links
      the payment, and a `thank_you` SMS with the next due date is queued.
- [ ] **P12.5** **Overpay path** — send a second proof for more than the
      contract's remaining balance and accept it. → the usual **409** confirm
      sheet appears, unchanged; confirming rolls over, cancelling writes nothing.
- [ ] **P12.6** **Reject** a third proof with a reason. → Asha's app shows the
      reason above **Send again**, and a `proof_rejected` SMS in her own language
      reaches her (`make api-log`), debited like any other kind.
- [ ] **P12.7** **Withdraw** — Asha submits a proof and withdraws it while it is
      unanswered → gone; withdrawing an accepted or rejected one is refused.
- [ ] **P12.8** **Reverse** the payment from P12.4 → the schedule reverts and the
      proof **stays accepted**, with the payment stamped reversed.
- [ ] **P12.9** Limits and isolation: a 6 MiB file and a `.docx` are refused; an
      11th proof in a day is refused with a `Retry-After`; as `demo@jjne.test`,
      opening a Load Test Estates proof id → **404**.
      *Evidence: renter hero, Proofs tab with badge, detail sheet, audit rows
      `proof.submit|accept|reject|withdraw`, api-log SMS lines.*

---

## P13. Import previous records (#14, flow 14)

- [ ] **P13.1** `{BASE}/tenant/settings/import` → kind picker (units, renters,
      payments) with the column reference and **Download template**; the template
      has English machine headers and one example line, in both UI languages.
- [ ] **P13.2** Upload a ~300-row **payments** file with two deliberately bad
      lines (an unknown unit, an amount over the contract balance). → the preview
      names the errors **on lines 14 and 87**, shows what every ok row resolved
      to, and **nothing is written** yet.
- [ ] **P13.3** "Commit N rows" without skipping → refused while errors remain;
      **"Skip 2 rows with errors and commit"** → the rest commit, the ledger shows
      the **imported** pencil chips, and the totals move by the file's sum.
- [ ] **P13.4** **Atomicity** — repeat with a file whose last line is invalid in
      a way only the commit can hit. → nothing at all is written.
- [ ] **P13.5** **Undo** the batch from history (within 24 h) → the payments are
      reversed with the reason "import undone", the chips go, and the schedules
      revert. A batch older than 24 h offers no Undo.
- [ ] **P13.6** **Units** kind: a file naming a property that does not exist →
      the preview flags that it will be created; commit → property and units
      appear, units `vacant`.
- [ ] **P13.7** **Renters** kind with a unit column → renters pre-registered and
      contracts created at **`pending_signature`** — **no contract is active**,
      and the `contract_ready` SMS goes out only at commit.
- [ ] **P13.8** A cell starting `=`, `+`, `-` or `@` survives verbatim in the app
      and is prefixed with `'` in every export. A 6 MiB file and a 6 000-row file
      are refused; a 21st preview in an hour is rate limited.
- [ ] **P13.9** Mobile: the preview table scrolls horizontally at 375 px with no
      page-level horizontal scroll. Cross-org: another org's `batch_id` → **404**.
      *Evidence: preview table with inline errors, ledger with imported chips,
      history with Undo, audit rows `import.preview|commit|undo`.*

---

## P14. Next payment due & payment instructions (#15, #16)

- [ ] **P14.1** Renter hero wording across states: "Due in 5 days", "Due today",
      "3 days overdue", "Awaiting confirmation" — in Kiswahili **and** English,
      in the stamp colours. The contract header and every Payments row carry the
      same chip.
- [ ] **P14.2** **How to pay** is pinned at the top of the Payments tab: bank,
      account name, account number with a working **copy** button, instructions,
      the reference hint "use your unit name", and the mobile-money block. It
      collapses only after the first view.
- [ ] **P14.3** Clear the org's bank account in Settings → the renter's card
      reads "Ask your landlord for payment details" rather than disappearing, and
      the landlord's dashboard nudge "Add payment instructions…" comes back.
      Restore the details, plus a **Mobile money** block; the settings page shows
      the live renter preview, and the block survives a `PATCH /org`.
- [ ] **P14.4** Landlord dashboard card **"Due in the next 7 days"** sits right
      after Overdue with a count, a total and the top 5 rows, linking to Payments
      → **Due soon** (14 days by default, 7 / 14 / 30 picker).
- [ ] **P14.5** Renters list **Next due** column sorts and tints overdue rows;
      the renter header shows Next due / Overdue; occupied units-board cards carry
      a "Due 3 Oct" chip.
- [ ] **P14.6** **Reconciliation** — the figures for one renter match
      `{BASE}/tenant/reports` → Payment status for the same window, and the
      upcoming list excludes waived, paid and finished contracts.
- [ ] **P14.7** **Midnight EAT** — a schedule due today reads "Due today" from
      00:00 EAT, and "1 day overdue" the following morning.
- [ ] **P14.8** A reminder SMS carries `{{pay_link}}`; tapping it on the phone
      opens the renter's Payments tab at the instructions card.
      *Evidence: hero in both languages, pinned card, dashboard card, Renters
      column, one reminder SMS body from `make api-log`.*

---

## Recording the run

Tick each box as you go, in Part 1 and Part 2 alike. For a failure, note the
flow or item number, the URL, what you expected, what happened, and the matching
lines from `make api-log`. Re-running
`make seed-demo` is always safe; to start the load fixture over,
`make seed SEED_ARGS=-reset`.
