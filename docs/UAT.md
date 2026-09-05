# UAT.md — user acceptance test script

Step-by-step acceptance run over every flow in [FLOWS.md](../FLOWS.md) (1–11),
against the seeded **JJnE Rentals** demo data.

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

There is no Beem account in dev, so every SMS — OTP codes included — is written
to the API log by the `LogProvider` instead of being sent.

```sh
make api-log                      # last 100 lines
tail -f .dev/api.log | grep sms_  # follow, OTPs only
```

Each send appears as one line:

```
level=INFO msg="sms (dev log provider)" sms_to=+255766000001 sms_sender=JJNE sms_body="Your TMS code is 483920..."
```

Take the code out of `sms_body`. OTP sends are rate-limited (3 per 10 minutes
per phone), so do not spam "resend".

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

## Recording the run

Tick each box as you go. For a failure, note the flow number, the URL, what you
expected, what happened, and the matching lines from `make api-log`. Re-running
`make seed-demo` is always safe; to start the load fixture over,
`make seed SEED_ARGS=-reset`.
