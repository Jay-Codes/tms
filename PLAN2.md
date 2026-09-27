# PLAN2.md — TMS Part 2 (post-MVP iteration)

Continues [PLAN.md](PLAN.md) (Phases 0–8, done 5 Sep 2026). Same rules: backend-first, real endpoints only, audit + org-scope on every mutation, Make + Git only, one feature branch per phase (`phase-N-name`), `PROGRESS.md` entry per phase, `DECISIONS.md` for spec-silent calls. SPEC.md / FLOWS.md / API.md are amended **before** code in each phase (TECHSTACK rule).

## Scope (client requests, 5 Sep 2026 — first round + end-to-end test feedback)

| # | Request | Phase |
|---|---------|-------|
| 1 | Time-series graphs + trends for revenue | 11 |
| 2 | Expense tracking and logging | 10 |
| 3 | Reports/trends with cadence selection: month, quarter, 6 months, annual, custom | 11 |
| 5 | Landlord app mobile-friendly | 9 (shell), 15 (full pass) |
| 6 | Landlord branding: choose app theme (background, accents, …) — presets + advanced override | 12 |
| 7 | Landlord left nav reloads on page change | 9 |
| 8 | "Recommended" shows on **every** payment period — align semantics | 9 |
| 9 | Renter contract page overflows on mobile (parties table values clipped) | 9 |
| 10 | Swahili/English **per user** preference (renter + landlord) → screen language + SMS/bulk-SMS language | 13 |
| 11 | Admin sets an **SMS balance limit** per landlord (org) | 14 |
| 12 | All message templates (EN + SW) **configurable on the admin page** | 14 |
| 13 | Renter uploads **proof of payment** (screenshot/PDF) to the landlord; landlord accepts → payment recorded | 16 |
| 14 | **CSV import** of previous records (units, renters, payment history) | 16 |
| 15 | **Next payment due** far more visible — landlord side (dashboard, renters, units) and renter side (home hero, countdown) | 16 |
| 16 | **Payment instructions** easy to find for renters (pinned, reachable from home, contract and proof sheet) | 16 |
| 17 | Contracts: **revoke, amend/update, re-sign, renew** | 17 (draft) |

Found during the same test pass (fixed in Phase 9): the contract terms say "TZS 100,000 per Quarterly (90 days)" while the unit price is 100,000 **per 30 days** — `{{rent}}` is not scaled to the payment period, so the signed document states the wrong figure.

Decisions taken with the client:
- **Theme:** curated presets by default + an "advanced" panel exposing individual tokens with live WCAG-AA contrast warnings. Stamp colors (Paid/Overdue) stay fixed platform-wide.
- **Expenses:** property-level ledger (optional unit), receipts, categories; plus an all-properties summary.
- **Revenue series:** collected (cash basis, non-reversed) vs expected (schedules due) vs expenses → net; period-over-period % change.

Confirmed with the client (5 Sep 2026, second round):
- **Recommended period:** exactly **one** period per org carries the badge (default Monthly); the other three bootstrap periods are "presets" (restorable) without a badge. Landlord selects which period is recommended in Settings → Periods. **Confirmed.**
- **Rent in the document:** `{{rent}}` becomes the amount **per payment period** (unit price × payment_period_days ÷ rent_period_days, same rounding as the schedule); new `{{rent_basis}}` variable keeps the unit price ("TZS 100,000 / 30 days") for landlords who want both. Header "Rent" row shows the per-payment-period figure with the basis underneath. **Confirmed: rent scales by payment period.**
- **Language:** stored on the user (`users.locale`, `sw|en`), chosen at registration/signup and changeable in Profile/Settings. Renter locale drives every SMS to that renter (including bulk); landlord locale drives the landlord screens. Org `sms_language` remains only as the fallback for renters with no preference (pre-existing users). Public QR landing / connect pages before login default to the org language and show a **SW/EN toggle** (confirmed); the choice carries into registration as the initial `locale`.
- **SMS balance:** prepaid credit per org set by the platform admin (top-ups, **no expiry, no monthly reset — confirmed**). Every outbound SMS except OTP/security messages debits 1 credit per 160-char segment. At 0 the queue holds messages as `held_no_credit`; landlord sees the balance and a low-credit warning in Notifications.
- **Templates:** platform defaults move from Go constants to a DB table the admin edits (EN + SW per kind, variables validated). Resolution order: org override → admin platform template → built-in code fallback. **Lock (confirmed):** per-kind `locked` flag set by the admin; `otp` locked out of the box; locked kind → landlord sees read-only wording, `PUT /org/notification-settings` override returns 409 `template_locked`.

---

## Phase 9 — Foundations + end-to-end fixes (1 day) — ✅ done 5 Sep 2026

- [x] **SPEC amendments**: §2.0 theme model (presets + token override, contrast rule, stamps fixed); §4 new tables `expenses`, `expense_categories`, `org_themes`, `platform_templates`, `org_sms_credits`, `sms_credit_ledger`, `users.locale`; §5.11 expenses API, §5.9 reports v2 (cadence param), §5.2 theme endpoints, §5.12 admin templates + credits, §5.13 locale; §6 rent-per-period rule and single-recommended rule; §7 bucket `receipts`. FLOWS: flow 12 "Expenses", flow 9 updated (cadence + trends), flow 1 branding + language step, flow 13 "Admin: templates & credits", flow 3/5 language pick. API.md "Part 2" section.
- [x] **Fix #7 — nav remount**: move `<Shell>` from the 25 tenant pages into a route-group layout `apps/tenant/app/(portal)/layout.tsx` (auth pages stay outside). Shell state (pending badges, org branding, theme) fetched once and kept across navigation; badges refresh on focus/interval, not on route change. Same pattern applied to `apps/admin`. Build-time check (`make lint`) fails if any page imports `Shell` directly.
- [x] **Fix #8 — recommended period**: migration `000012` adds partial unique index `payment_periods (org_id) WHERE is_recommended AND deleted_at IS NULL`; data fix keeps Monthly (or the lowest-days period) recommended, clears the rest; org bootstrap and seed set only Monthly; `POST /org/payment-periods/{id}/recommend` moves the badge (audited); Settings → Periods gets a "Set as recommended" action; renter unit page + connect keep "Recommended first" ordering (now meaningful).
- [x] **Fix #9 — contract overflow (renter + landlord document)**: parties table cells drop `nowrap` (`.ledger.doc-parties td.num { white-space: normal; overflow-wrap: anywhere }`), long values wrap right-aligned; phone mask stays on its own line; term dates use a non-breaking en-dash pair; `.doc` gets `min-width: 0` inside the grid. Verified at 320/375/414 px in the enduser app and in the tenant contract page + print.
- [x] **Fix — rent per payment period in the document**: `contract.RentPerPeriod(rentAmount, rentPeriodDays, paymentPeriodDays)` (shared with `Generate`), `{{rent}}` = per-payment-period amount, new `{{rent_basis}}`; default template sentence becomes "rent of **{{rent}}** per {{payment_period}} ({{rent_basis}})"; renter + landlord "Rent" rows show both. Existing signed contracts are untouched (snapshot rule); an "as displayed" note is added to DECISIONS. Hash unaffected (hash covers terms HTML, which is rendered before hashing).
- [x] **Responsive shell (#5 base)**: tenant `Shell` gets a collapsible left nav → bottom bar / hamburger under 768px; tables get an `overflow-x` wrapper + card fallback for narrow widths; sheets become full-screen on mobile; touch targets ≥44px (already in tokens).
- [x] **Shared `PeriodPicker`** component (`packages/ui`): cadence `month | quarter | half_year | year | custom` + navigation (prev/next), emits `{from, to, cadence}`; used by every report and dashboard card in Phase 11.
- [x] Backend: `internal/period` package — resolves cadence + anchor date to `[from, to)` in EAT (reuse `internal/tz`), plus bucket size for series (`day` for ≤ 62 days, `week` ≤ 26 weeks, `month` otherwise, overridable).
- [x] Migrations `000012_part2_foundations` (tables below, empty for now; `users.locale`), bucket `receipts` in compose init.

**Exit:** navigating between tenant pages keeps the nav mounted (no refetch flash); only one period shows "Recommended"; the JJnE test contract shows "TZS 300,000 per Quarterly (90 days) (TZS 100,000 / 30 days)"; the renter contract fits at 375px with no clipped text; `PeriodPicker` renders in the design-system page; tests green.

## Phase 10 — Expenses (1 day) — ✅ done 5 Sep 2026

Schema:
```
expense_categories  org_id, name, is_default, sort_order, active            -- seeded: Repairs & maintenance, Utilities, Security, Cleaning, Taxes & levies, Insurance, Management fees, Other
expenses            org_id, property_id, unit_id NULLABLE, category_id, amount (TZS), incurred_on (date),
                    vendor, reference, note, receipt_object_key NULLABLE, recorded_by_user_id,
                    status (recorded|voided), voided_at, void_reason, deleted_at
```
- [x] API (`/expenses`, `/org/expense-categories`): CRUD categories; `POST /expenses` (validation: amount bounds, date ≤ today+1, property in org, unit belongs to property), `PATCH` (same day edits allowed; audited before/after), `POST /expenses/{id}/void {reason}` (append-style correction, like payment reverse), `GET /expenses?property_id=&unit_id=&category_id=&from=&to=&cursor=` + `format=csv`, receipt presigned upload/complete/view (bucket `receipts`, `{org_id}/{expense_id}.{ext}`, ≤5 MiB, image/pdf), `GET /expenses/summary?cadence=&from=&to=&group_by=property|category` → totals per group + grand total (all-properties view).
- [x] Tenant screens: **Expenses** nav item → ledger (PeriodPicker, property/category filters, totals with double rule, CSV export), "Record expense" sheet (with receipt upload), expense detail (receipt viewer, void with reason), per-property tab on the property page, settings → expense categories manager.
- [x] Reports hook: expenses feed Phase 11 net figures.
- [x] Tests: validation, void restores nothing (append semantics), CSV formula-neutralised, isolation (org B → 404 on every expense route; census table extended — build fails otherwise), summary math by property/category.

**Exit:** landlord records an expense with receipt on Mbezi Beach Block A, sees it in the property tab and in the all-properties summary; void audited; CSV downloads.

## Phase 11 — Reports v2: cadence + time-series + trends (1 day) — ✅ done 5 Sep 2026

- [x] **Cadence everywhere (#3)**: `cadence=month|quarter|half_year|year|custom&from&to&anchor` accepted by `/reports/summary`, `/reports/payment-status`, `/reports/collections`, `/expenses/summary`; response echoes the resolved `{from,to,cadence}` and a `previous` window for comparison.
- [x] **Revenue series (#1)**: `GET /reports/revenue?cadence=&from=&to=&bucket=&property_id=` → `{buckets:[{start,expected,collected,expenses,net}], totals, previous_totals, change_pct:{collected,expenses,net}, trend:{slope_collected_per_bucket}}`. Cash basis (non-reversed payments by `paid_at`), expected = schedules due in bucket (excl. waived), expenses by `incurred_on` (excl. voided). Zero-filled buckets; ≤ 400 buckets.
- [x] **Per-property breakdown**: `group_by=property` variant for revenue + expenses; occupancy series `GET /reports/occupancy?cadence…` (units occupied per bucket end).
- [x] Tenant **Reports** rework: Overview (period tiles + Δ vs previous period), **Revenue** tab (line/area chart collected vs expected, bars expenses, net line; hover tooltips; per-property toggle), Expenses tab (stacked bars by category + table), Payment status, Collections; every tab driven by the shared `PeriodPicker`; charts as inline SVG in `packages/ui/charts` (no chart lib; follow the dataviz skill for color/legend/axis rules; dark/light aware).
- [x] Dashboard cards: "Revenue this period" sparkline + Δ%, "Net income", "Expenses" card added to `dashboard_prefs.cards`.
- [x] Tests: period resolver (month/quarter/half-year/year boundaries in EAT, custom validation, max range 5 years), bucket auto-sizing, revenue math on seeded org (reconcile totals with `/payments` and `/expenses` sums), previous-period comparison, per-property sums equal grand total.

**Exit:** Reports show revenue vs expected vs expenses vs net over any cadence incl. custom range, with % change vs previous period; per-property breakdown; seed data produces a visibly correct chart.

## Phase 12 — Theming v2: presets + advanced override (1 day) — ✅ done 5 Sep 2026

- [x] **Token model** (`packages/ui`): expose the themable set — `paper` (background), `surface` (sheets/cards), `ink` (text), `ink-muted`, `rule` (ledger lines), `primary` (+ derived pressed/tint/on-primary), `accent`, `font`. Fixed forever: stamp colors, focus ring, spacing, radii, type scale.
- [x] **Presets** (8): Ledger (current default), Night ledger (dark), Warm paper, Cool slate, Forest, Ocean, High-contrast, Minimal white — each a full token set validated for WCAG AA.
- [x] **Contrast guard**: `packages/ui/theme` `validateTheme(tokens)` → list of failing pairs (text/paper, muted/paper, on-primary/primary, ink/surface) with ratios; backend re-validates on save and rejects < 4.5:1 for body text (400 with the failing pairs).
- [x] API: `GET/PUT /org/branding` gains `theme: {preset_id|null, tokens:{...}|null, font_id}`; `/public/orgs/{slug}/branding` and `/public/units/{code}` return the resolved token set; `GET /themes/presets` (public) lists presets.
- [x] Tenant **Branding** screen: preset gallery with live preview (renders a mini ledger + stamp + button with the candidate theme), "Advanced" panel (color inputs per token, live contrast badges, reset to preset), applies immediately to the tenant app; enduser app applies the same resolved theme on org pages (QR landing, connect, contract, rent book).
- [x] `applyOrgTheme()` v2 sets all tokens; falls back to preset "Ledger" when the org has none; admin app never themed.
- [x] Tests: validator table (passing/failing pairs), backend rejection, presets all pass AA, public endpoints resolve tokens; visual check of each preset on the design-system page in light + dark system settings.

**Exit:** JJnE picks "Night ledger", tweaks accent in advanced, saves; tenant app + renter QR landing reflect it; a low-contrast choice is blocked with a clear message.

## Phase 13 — Language: Swahili/English per user (#10) (1 day) — ✅ done 6 Sep 2026

- [x] **Data**: `users.locale TEXT NOT NULL DEFAULT 'sw' CHECK (locale IN ('sw','en'))` (Phase 9 migration). Renter registration (`POST /auth/renter/register`) and landlord signup accept `locale`; `PATCH /me` (renter) and `PATCH /org/members/me` (org user) update it; `GET /me` / `GET /auth/session` return it. Audited.
- [x] **SMS resolution**: `notify.LanguageFor(recipientUser, org)` = user locale → org `sms_language` fallback. Every queue writer (link approved/rejected, contract ready/terminated, welcome, thank-you, reminders, overdue, unsigned, OTP) passes the recipient's locale, not the org's. Scheduler groups by recipient locale. Org settings "SMS language" is relabelled "Default language for renters without a preference".
- [x] **Bulk SMS (#10)**: `POST /notifications/custom` (existing bulk endpoint) accepts `{body_sw?, body_en?, recipients…}` (at least one); each recipient gets the body matching their locale, falling back to the other when only one is given; the compose screen shows two tabs (SW/EN) with a recipient-count per language and a per-recipient preview. Delivery log stores the language used.
- [x] **UI i18n**: `packages/ui/i18n` — `en.ts`/`sw.ts` dictionaries, `I18nProvider` + `useT()` with ICU-style plural/number/date helpers (EAT, TZS); every user-facing string in `apps/enduser` and `apps/tenant` moved to keys (admin stays English). Language follows the signed-in user's locale; before sign-in public pages default to the org language with a visible SW/EN toggle (persisted in localStorage, prefilled into registration); a switcher sits in the renter Profile and landlord Settings → Preferences (also in the auth pages' footer). `<html lang>` updated. Swahili copy reviewed for the ledger vocabulary (Kodi, Risiti, Imelipwa, Imechelewa…).
- [x] Contract templates: default template ships in both languages (`body_html_sw`, `body_html_en`); `POST /contracts` renders the renter's locale (landlord can override per contract); snapshot records `language`. Existing templates untouched.
- [x] Tests: language resolution table (user/org combos), bulk fan-out counts per language, dictionary completeness check (`make lint` fails on a key missing in either language), OTP always in recipient locale.

**Exit:** renter Asha sets Kiswahili → app renders in Swahili and her reminders arrive in Swahili while a renter with English gets English from the same bulk send; landlord toggles English → tenant app in English; no untranslated key in either app.

## Phase 14 — Admin: SMS credits per org + platform templates (#11, #12) (1 day) — ✅ done 6 Sep 2026

Schema:
```
org_sms_credits     org_id PK, balance INT NOT NULL DEFAULT 0, low_watermark INT NOT NULL DEFAULT 50, updated_at
sms_credit_ledger   id, org_id, delta INT, balance_after INT, reason (topup|adjust|debit|refund), notification_id NULLABLE,
                    admin_user_id NULLABLE, note, created_at                      -- append-only (trigger)
platform_templates  kind PK, sw TEXT, en TEXT, variables TEXT[], updated_by_admin_id, updated_at, version INT
platform_template_versions  kind, version, sw, en, admin_user_id, created_at    -- history, append-only
```
- [x] **Credits (#11)**: admin `GET /admin/orgs/{id}/sms` → `{balance, low_watermark, used_30d, held_count, ledger:[…]}`; `POST /admin/orgs/{id}/sms/topup {credits, note}`, `POST …/adjust {delta, note}`, `PATCH …/sms {low_watermark}` — all audited (platform audit + org audit visible to the landlord as "credits added by platform"). Worker debits atomically **at send time** (`UPDATE … SET balance = balance - n WHERE balance >= n`), one credit per 160-char GSM segment (70 for UCS-2); OTP/security kinds exempt (configurable list, default `otp`). Insufficient balance → row status `held_no_credit` (not `failed`); a top-up releases held rows in order. Bulk send pre-checks (`409 insufficient_sms_credits {needed, balance}`) so the landlord is told before queuing.
- [x] Landlord visibility: `GET /org/sms-credits` → `{balance, low_watermark, held_count}`; Notifications page header shows balance + "N messages held" with a "contact platform" note; low-balance in-app banner under the watermark; optional email to the owner (log provider in dev).
- [x] **Platform templates (#12)**: `GET /admin/templates` (all kinds, both languages, variables, version, last editor), `PUT /admin/templates/{kind} {sw, en}` (validates placeholders against the kind's allowed variables, ≤ 3 segments warning, records a version; audited), `POST /admin/templates/{kind}/preview {language, sample?}`, `POST /admin/templates/{kind}/revert {version}`, `PATCH /admin/templates/{kind} {locked}`; `platform_templates.locked BOOL NOT NULL DEFAULT false` (otp seeded true). `notify.Render` resolution: org override → platform_templates row → built-in Go default (seeded into the table by migration so the table is authoritative from day one). Rendered cache in Redis invalidated on save.
- [x] Admin app: **Orgs → org detail → SMS** tab (balance, top-up form, ledger, held messages); **Templates** nav item (list by kind with SW/EN editors side-by-side, variable chips, live preview with sample values, version history + revert). Admin dashboard metric: credits consumed today / orgs under watermark.
- [x] Tests: debit atomicity under concurrency (race test), segment counting (GSM vs UCS-2, Swahili diacritics), held→released ordering after top-up, exemption list, template validation (unknown variable rejected, both languages required), resolution precedence, isolation (org B cannot read org A credits).

**Exit:** admin tops up JJnE with 100 credits and lowers the watermark; a 120-recipient bulk send is refused with the shortfall; after top-up the held rows go out; admin edits the Swahili `reminder_due` template, previews it, and the next reminder uses the new wording; history shows the previous version.

## Phase 15 — Mobile landlord pass, hardening, UAT 2 (1 day) — ✅ done 6 Sep 2026 (UAT 2 phone run pending the ngrok tunnel + approved Beem sender ID)

- [x] **Mobile landlord (#5 full)**: every tenant screen audited at 375/414/768 px: properties, units board (card view), link inbox, renters, contracts + document (print unaffected), payments (record sheet full-screen), expenses, reports (charts scale, tables scroll), settings, branding, notifications (credits header, bulk compose SW/EN), audit. Bottom bar for the 5 most-used sections; sheets slide from bottom; sticky action bars respect safe areas. PWA install banner on tenant app. Renter app re-checked in both languages (Swahili strings are longer).
- [x] Performance: reports v2 endpoints in `make loadtest` (p95 < 300 ms on seed org); indexes for `expenses (org_id, incurred_on)`, `(org_id, property_id, incurred_on)`, `payments (org_id, paid_at)`, `notifications (org_id, status) WHERE status='held_no_credit'`.
- [x] Isolation census covers all new routes (build fails otherwise); rate limits on receipt uploads and admin template edits; CSV neutralisation on expenses export.
- [x] Seed v2: `make seed` adds 12 months of expenses per property and payments spread over a year so trends are meaningful, renters split SW/EN, 500 credits per org; `make seed-demo` adds JJnE expenses, a custom theme, and an edited platform template.
- [x] Docs: SPEC/FLOWS/API/README/DEV updated; `docs/UAT.md` Part 2 section (flows 9 v2, 12 expenses, 13 admin templates/credits, branding v2, language switch, mobile checklist).

**Exit:** UAT 2 checklist executed clean on the ngrok preview from a phone (landlord + renter, both languages); `make build/test/lint/test-isolation/loadtest` green; PROGRESS.md entries for 9–15.

---

## Phase 16 — Proof of payment, CSV import, due-date visibility (#13–#16) (2 days) — ✅ done 20 Sep 2026

Order inside the phase: docs → schema → proofs API → import API → visibility endpoints → tenant screens → renter screens → tests. Branch `phase-16-proofs-import`.

### 16.0 Docs first (TECHSTACK rule)
- [x] SPEC: §4 tables `payment_proofs`, `import_batches`, `import_rows`; §5.7 proofs API; §5.14 imports API; §5.9 `GET /reports/upcoming`; §7 bucket `proofs`; §6 rule "a proof is a claim, a payment is a fact — only an accepted proof touches a schedule". FLOWS: flow 7 gains "renter sends proof → landlord accepts/rejects"; new flow 14 "Import previous records"; flow 8 note on `proof_rejected` SMS. API.md "Part 2 — Phase 16" section. DECISIONS for every spec-silent call below.

### 16.1 Proof of payment (#13)
Schema:
```
payment_proofs   id, org_id, contract_id, schedule_id NULLABLE, renter_user_id, amount BIGINT CHECK > 0,
                 paid_at TIMESTAMPTZ, method (bank_transfer|mobile_money_manual), reference NULLABLE (≤80),
                 note NULLABLE (≤500), object_key, content_type, size_bytes,
                 status (submitted|accepted|rejected), payment_id NULLABLE REFERENCES payments,
                 reviewed_by_user_id NULLABLE, reviewed_at NULLABLE, rejection_reason NULLABLE (≤200),
                 created_at, updated_at
                 -- index (org_id, status, created_at DESC); partial index WHERE status='submitted'
```
Bucket `proofs`, key `{org_id}/{proof_id}.{ext}`, `image/jpeg` / `image/png` / `application/pdf`, ≤ 5 MiB, private, presigned reads for the org and the submitting renter only (same shape as `receipts`).

- [x] **Renter API**: `POST /me/proofs/upload {contract_id, content_type, size_bytes}` → presigned PUT ticket; `POST /me/proofs {contract_id, schedule_id?, amount, paid_at, method, reference?, note?, object_key}` → `201 {proof}` status `submitted` (verifies the object exists and matches size/type — the completion callback pattern from receipts); `GET /me/proofs?cursor=` → own proofs with status, reviewer note; `DELETE /me/proofs/{id}` only while `submitted` (withdraw). Rate limit 10 proofs / renter / day. Contract must be `active|expiring` and belong to the renter → else 404/409 `contract_not_active`.
- [x] **Landlord API**: `GET /proofs?status=submitted|accepted|rejected&cursor=` (default `submitted`, ascending by `created_at` — oldest claim first); `GET /proofs/{id}` (+ `view_url` presigned, short TTL); `POST /proofs/{id}/accept {amount?, schedule_id?, paid_at?, allow_overpay_rollover?}` → runs the existing `POST /payments` allocator with the proof's fields (overrides allowed, audited as before/after), links `payment_id`, status `accepted`, queues the existing `thank_you` SMS; **409** `overpay_confirm_required` / `exceeds_contract_balance` propagate unchanged so the UI reuses the confirm sheet; `POST /proofs/{id}/reject {reason}` → status `rejected`, SMS `proof_rejected` (new kind, SW/EN, platform template + org override, `{{reason}}` platform-only as today). Reversing an accepted proof's payment (existing `POST /payments/{id}/reverse`) leaves the proof `accepted` with the payment stamped reversed — the record stays. Audit actions: `proof.submit`, `proof.withdraw`, `proof.accept`, `proof.reject`, `proof.view`.
- [x] **Renter screens** (`apps/enduser`): "Send proof of payment" button on the home hero and on the Payments tab (pre-filled with `next_due` amount + schedule); sheet = amount, date, method, reference, photo/PDF picker (camera on mobile), the org's **payment instructions repeated inside the sheet**; on submit the schedule row shows an "Awaiting confirmation" pencil chip until accepted/rejected; a rejected proof shows the reason with "Send again". Proof history under Payments → History.
- [x] **Landlord screens** (`apps/tenant`): Payments page gains a **Proofs** tab (first tab when `submitted_count > 0`, badge in nav + bottom bar, dashboard card `proofs` added to the card allowlist); row = renter, unit, claimed amount vs outstanding, date, thumbnail; detail sheet = full image/PDF viewer, side-by-side "claimed" vs "schedule expects", Accept (opens the Record-payment sheet pre-filled, so the overpay confirm and reverse flows are unchanged) / Reject with reason. Renter detail page and contract page list the renter's proofs.
- [x] **Notifications**: kind `proof_rejected` added to `notify.Render`, platform template seeded (SW/EN), admin template editor picks it up automatically, org override allowed, credits debited like any other kind. No SMS on submit (the landlord uses the dashboard, same rule as signing) — the in-app badge is the signal.
- [x] Tests: presign/complete size+type enforcement; renter cannot submit for another renter's contract (404); org B cannot read org A's proofs (census extended, build fails otherwise); accept → payment allocated and `payment_id` linked, schedule flips paid; accept with overpay → 409 passthrough; reject → SMS queued with reason; withdraw only while `submitted`; rate limit 429.

### 16.2 CSV import of previous records (#14)
Schema:
```
import_batches   id, org_id, kind (units|renters|payments), filename, row_count, ok_count, error_count,
                 status (previewed|committed|undone), created_by_user_id, committed_at, undone_at, created_at
import_rows      id, batch_id, org_id, line INT, raw JSONB, errors JSONB NULLABLE,
                 entity_type NULLABLE, entity_id NULLABLE            -- what the row became on commit
```
- [x] **API** (`/imports`, landlord, owner/manager): `GET /imports/templates/{kind}.csv` → header row + one example line (SW/EN header labels are *not* used — fixed English machine headers, documented on the page); `POST /imports/preview` multipart `file` + `kind`, ≤ 2 MiB, ≤ 5 000 rows, UTF-8/BOM tolerant, `,` or `;` delimiter sniffed → `201 {batch_id, rows:[{line, raw, errors, resolved:{…}}], ok_count, error_count}` — nothing written but the batch; `POST /imports/{batch_id}/commit` → runs every ok row in **one transaction** (all-or-nothing — a landlord must never end up with half a spreadsheet), rows with errors are refused unless `skip_errors=true`; `POST /imports/{batch_id}/undo` within 24 h → payments reversed with reason `import undone`, units/renters created by the batch soft-deleted when untouched since; `GET /imports?cursor=` history. Rate limit 20 previews / h. Audited `import.preview`, `import.commit`, `import.undo` (with batch id, counts).
- [x] **Kinds and columns** (validated per row, same rules as the manual endpoints — the import calls the same service functions, never raw SQL):
  - `units`: `property, unit, rent_amount, rent_period_days?, status?` → property matched by name (created when missing, flagged in preview), unit created `vacant`/`unlisted`.
  - `renters`: `full_name, phone, locale?, property?, unit?` → user pre-registered by phone (existing "known to org" path); when `unit` is given, a `link_request` is created `approved` → contract `pending_signature` from the org default template, `contract_ready` SMS held until commit; the landlord activates via the existing FLOWS 3.6 countersign path when the renter cannot sign. No contract is ever activated by an import.
  - `payments`: `unit, renter_phone, amount, paid_at, method, reference?, note?` → contract resolved by unit + renter phone (must be `active|expiring|ended|terminated` — history may belong to a finished contract), allocated by the existing allocator in `paid_at` order with `allow_overpay_rollover=true`; a row that exceeds the contract balance is an error in preview (the landlord fixes the sheet or the contract dates). `method` limited to the three manual values. Payments carry `import_batch_id` (new nullable column, migration) and show an "imported" pencil chip in ledgers.
- [x] **Tenant screens**: Settings → **Import data** page: kind picker with column reference and template download, drop zone, preview table (row status, inline errors, resolved property/unit/renter names), "Commit N rows" / "Skip M rows with errors and commit", history list with Undo (24 h). Mobile: preview table scrolls horizontally (Phase 15 pattern).
- [x] Tests: delimiter + BOM sniffing; formula-prefix cells are stored verbatim but neutralised on every export (existing rule); every column validation error lands on the right line; commit is atomic (inject a failure on row N → nothing written); payments allocation order; undo reverses only the batch's payments; org B cannot see org A batches; 5 000-row batch under 5 s on the seed org.

### 16.3 Next payment due — visibility (#15)
- [x] **API**: `GET /me/schedules` adds `next_due.days_until_due` (negative when past) and `next_due.proof:{id,status}|null`; new `GET /reports/upcoming?days=7|14|30&property_id=` → `{items:[{schedule…, renter_name, phone, unit_name, property_name, days_until_due}], total_due, count}` sorted by due date; `GET /renters` list rows gain `next_due_date, next_due_amount, overdue_amount`; `GET /units` board rows gain `next_due_date` for occupied units.
- [x] **Renter app**: home **hero block** replaces the small ledger row — large due date, amount, a countdown chip ("Due in 5 days" / "Due today" / "3 days overdue" in the stamp colours, "Awaiting confirmation" when a proof is pending), two full-width buttons **How to pay** (jumps to the pinned instructions card) and **Send proof**; contract page shows the same chip in its header; Payments tab keeps the chip on every row. Countdown wording SW/EN.
- [x] **Landlord app**: dashboard card `upcoming` ("Due in the next 7 days": count + total, top 5 rows, link to Payments → Due soon) added to the allowlist and to the default card order right after `overdue`; Renters list gains a **Next due** column (sortable, overdue tinted); renter detail header shows Next due / Overdue figures above the tabs; units board card chip "Due 3 Oct" on occupied units; Payments "Due soon" tab default window 14 days with a 7/14/30 picker.
- [x] Tests: `days_until_due` across EAT midnight; upcoming report excludes waived/paid and finished contracts; renter list figures match `/reports/payment-status` for the same renter (reconciliation test).

### 16.4 Payment instructions — findability (#16)
- [x] **Renter app**: "How to pay" card **pinned at the top** of the Payments tab (bank, account name, number with copy, instructions text, reference hint "use your unit name"), collapsible only after the first view; reachable from the home hero button, from the contract page ("How to pay" link under the rent row) and repeated inside the proof sheet; when the org has no bank account set, the card says "Ask your landlord for payment details" instead of disappearing.
- [x] **Landlord app**: setup checklist / dashboard nudge "Add payment instructions so renters know where to pay" until `bank_account` is set (dismissable, returns if cleared); Settings → Bank account page shows a live **renter preview** of the card; optional second block "Mobile money" (`mobile_money:{provider, number, name}`) stored beside `bank_account` in `orgs.settings`, shown on the same card.
- [x] **SMS**: `{{pay_link}}` variable (deep link to `/enduser/payments`) added to the org template whitelist and used by the platform defaults of `reminder_7d`, `reminder_due`, `overdue_daily`.
- [x] Tests: `mobile_money` round-trips through `PATCH /org` untouched; `{{pay_link}}` renders per org base URL; whitelist validation accepts the new variable and still rejects unknown ones.

**Exit:** renter Asha sees "Due in 5 days · TZS 100,000" on her home screen with How-to-pay and Send-proof buttons, uploads a screenshot, JJnE sees it in the Proofs tab with a badge, accepts it → schedule paid, thank-you SMS queued with next due; a rejected proof reaches her with the reason. JJnE imports a 300-row payments CSV: preview shows 2 errors on lines 14 and 87, commit with skip, ledger shows imported chips, undo reverses them. Dashboard "Due in 7 days" card and the Renters "Next due" column agree with the payment-status report. `make build/test/lint/test-isolation` green, census extended, PROGRESS entry.

---

## Phase 17 — Contracts: revoke, amend, re-sign, renew (#17) — 📝 draft, to discuss with the client before scoping

What exists today: `POST /contracts/{id}/terminate` (works from `pending_signature`, `active`, `expiring` — cancelling an unsigned contract is the same call), the `landlord_recorded` countersign path, `expiring` at ~30 days, and the rule that a signed document is **never edited** (snapshot hash, FLOWS 3 edge case: "landlord terminates and issues a new contract"). Anything below must keep that rule: a change is always a **new document** the renter signs again.

Proposed model — **supersession**:
```
contracts  + supersedes_contract_id NULLABLE, superseded_by_contract_id NULLABLE, amendment_reason NULLABLE,
           + status value `superseded`
```
- **Amend** (`POST /contracts/{id}/amend {changes:{rent_amount?, payment_period_id?, due_day?, end_date?, unit_id?}, effective_date, reason}`): creates a new `pending_signature` contract pre-filled from the old one with the changes applied, `supersedes_contract_id` set, terms re-rendered from the current template, SMS `contract_ready`. The old contract stays `active` and keeps collecting until the new one **activates**; activation then marks the old one `superseded` (not terminated), waives its schedules with `period_start ≥ effective_date`, carries paid/partial rows across as credit on the first new schedule, and moves the unit if `unit_id` changed. Only one open amendment per contract (409 `amendment_pending`).
- **Renew** = amend with only `end_date` (and optionally `rent_amount`) from an `expiring` contract; the "Renew" button on the contract page is this call. Replaces the FLOWS 4 "renew (new contract, current price)" wording.
- **Revoke** = the existing terminate from `pending_signature` (already there), renamed in the UI to "Withdraw" so it is not confused with terminating a live tenancy. For an `active` contract with wrong terms: amend (renter re-signs) — or terminate + new contract when the tenancy itself is wrong. No silent edit path, ever.
- **Re-sign** (recovery): `POST /contracts/{id}/reissue {reason}` for a `pending_signature` contract whose snapshot is stale (`snapshot_mismatch` on sign/activate, template changed, phone changed): withdraws it and creates a fresh one from the same parameters, new SMS. Also the answer to "renter never signed and the link expired".
- **Landlord countersign correction**: none — a landlord signature error is a reissue.
- **Document**: the contract document shows "Supersedes contract #… from {date}" / "Superseded by #… on {date}" in the header; both stay printable and verifiable. The renter app lists the chain under Contract.
- **Audit**: `contract.amend`, `contract.reissue`, `contract.supersede` with before/after terms; the org audit filter gains these kinds.

Open questions for the client (defaults applied if unanswered):
1. Amendment takes effect **only after the renter signs** (default) vs landlord-only amendment for rent changes with an SMS notice? Default: renter signs, always.
2. Carry-over of money already paid past `effective_date`: credit on the new contract's first schedule (default) vs refund recorded as an expense.
3. Can a **renter** request an amendment (e.g. shorter term) or is it landlord-initiated only? Default: landlord only in this phase.
4. Rent increase cap / notice period (Tanzanian practice: written notice, commonly 30–90 days): enforce `effective_date ≥ today + notice_days` (org setting, default 30) or just warn? Default: warn.
5. Renewal reminders: reuse `expiring` timing (30 days) with a new SMS kind `renewal_offer` carrying the link to sign, or keep landlord-driven? Default: landlord-driven, no new SMS kind.

---

## Phase 18 — Landlord-assisted onboarding (OTP fallback, in person) (1 day) — 🔨 in progress, 20 Sep 2026, branch `phase-18-assisted-onboarding`

Why: Beem accepted the OTP but left it `pending`; the renter never got a code and had no way forward. Landlord and renter are usually in the same room at onboarding (QR on the door), so the landlord's screen becomes the code channel. No SMS, no provider dependency. FLOWS 2b, SPEC §3 / §4 / §5.15.

### 18.0 Docs first
- [x] FLOWS 2b + Flow 3.5 note; SPEC §3 bullet, §4 `assist_sessions`, `contract_signatures.witnessed_by_user_id`, `notification_log.channel in_person` / status `shown`; §5.15 API; API.md "Phase 18" section; DECISIONS rows.

### 18.1 Backend
- [x] Migration `000020_assist_sessions`: table per SPEC §4 (`UNIQUE (org_id, phone) WHERE status='open'`), `contract_signatures.witnessed_by_user_id UUID NULL REFERENCES users`, widen `notification_log.channel` CHECK to `('sms','in_person')` and `status` to include `shown`.
- [x] `auth.Store`: `PutOTPAssisted(purpose, phone, code)` — overwrites the slot, re-arms TTL, **ignores** cooldown; `SetAssistMarker(purpose, phone, sessionID)` / `TakeAssistMarker` (TTL = OTP TTL) and `SetWitnessMarker(contractID, userID)` / `TakeWitnessMarker`.
- [x] Handlers (org audience, owner + manager): `POST /assist`, `GET /assist`, `GET /assist/{id}`, `POST /assist/{id}/code`, `POST /assist/{id}/close`, `POST /contracts/{id}/witness-otp`; public `GET /public/assist/{id}`. Per-org limiter `assist:issue:{org}` 30/h; per session 10 codes. Purpose = `login` when `GetUserByPhone` finds a renter, `register` when none, 409 `not_a_renter_phone` for staff/admin, 409 `assist_open` when another open session exists (response carries its id). Session expiry 30 min from open (extended on each new code).
- [x] Hooks: `handleOTPVerify` (register/login) and `handleRegisterRenter` — if an assist marker exists for (purpose, phone) → stamp `renter_user_id`, audit `after.assist_session_id`. `handleCreateLinkRequest` — open session for (org, phone/user, unit) → stamp `link_request_id`. `handleSignContract` — witness marker → `witnessed_by_user_id` on the signature row. `GET /assist/{id}` derives `status_detail` from the stamps and the link request status.
- [x] `notification_log` row per issue: `kind=otp, channel=in_person, status=shown, body=""` (the code is never persisted), `dedupe_key=assist:{session}:{n}`; credits untouched. Admin metrics / failed-sends unaffected.
- [x] Audit: `renter.assist_start`, `renter.assist_code` (every issue and refresh, `after.n`), `renter.assist_close`, `contract.witness_otp` (entity contract). Org audit filter kinds updated.
- [x] Tests: assisted code verifies through the normal endpoint; assisted issue overwrites an in-flight SMS code; cooldown bypass is assisted-only (SMS path still 429s); per-org limiter 429; staff phone refused; second open session 409; org B cannot read/refresh org A's session (isolation census); public lookup returns no phone; register stamps `renter_user_id`; link request stamps `link_request_id`; witness code → signature row carries `witnessed_by_user_id`, and the SMS-issued code does not; expired session refresh → 409 `assist_closed`.

### 18.2 Frontend
- [ ] Tenant: `renters/assist` page (code display + QR + countdown + New code; live status via 5 s polling; "Review & approve" deep link); entry from unit detail ("Onboard in person"), Renters list ("Add renter → In person") and renter detail ("Help log in"); contract page "Witness signing" sheet with the code. SW/EN strings.
- [ ] Enduser: `/u/{unit_code}?assist=` → register/login pages hide "Send code", show "Enter the code your landlord shows you", no resend; sign page reads the same hint. Session id kept in `sessionStorage` until the link request is created.
- [x] Enduser: **unit code input on renter home** — a renter who registered without scanning (bare `/register`, or landlord read the code aloud) currently sees only "scan your unit's QR code" with no way forward. Add a "Have a unit code?" field to the `home.hint.none` state: 10-char Crockford code, uppercased/trimmed client-side, submit → `router.push('/u/{code}')` (the unit page already handles 404 via `unit.notFound`). No backend change. SW/EN strings.
- [x] Enduser: **home hero per unit** — a renter with two live contracts (allowed: no renter-side uniqueness, only one live contract per unit) sees a single "next due" hero and a single proof-of-payment target, both taken from `GET /me/schedules` `next_due`; the second unit's rent is invisible on home until the first is paid. When `contracts` has more than one live row, render one due card per contract (earliest unpaid schedule of each, unit · property label, its own countdown chip and "Send proof" targeting that contract) instead of the single hero; one contract keeps today's layout. Client only: derive per-contract next due from `GET /me/schedules` `items`, no new endpoint. SW/EN strings.
- [x] Enduser: **renter history paging** — `GET /me/payments` and `GET /me/contracts` return 50 rows (max 200) with `next_cursor`, but the enduser app never reads it: a renter past 50 receipts silently loses the oldest. Payments page "History" and "Proofs you sent", and the contracts list, get a "Show older" button that appends the next page (`?cursor=`), hidden when `next_cursor` is null; `MyPaymentsResponse` / contracts `mine()` typed with the cursor. `GET /me/schedules` is unpaged by design (whole rent book) and stays so. SW/EN strings.
- [x] Enduser: **past vs current tenancies** — contracts list and the payments page schedule groups mix ended/terminated contracts with live ones. Contracts page: two sections, "Current" (`pending_signature`, `active`, `expiring`) and "Past" (`ended`, `terminated`), past collapsed by default with a count; each past row keeps its status mark and still opens the document (never edited after end, hash still verifies). Payments page: schedule groups whose `contract_status` is ended/terminated move under a collapsed "Past tenancies" heading; overdue total and next-due hero count live contracts only. Client only, uses `contract_status` already on `/me/schedules` rows. SW/EN strings.
- [x] Docs: API.md confirmed contract, PROGRESS entry, DECISIONS, UAT checklist row.

---

## Phase 19 — Identity visibility: NIDA reveal for landlords, platform user directory — ✅ done 20 Sep 2026

Why: a landlord approving a tenancy or chasing a defaulter needs the renter's actual NIDA number (for a police report, a guarantor check, a lease filed with the ward office); today every landlord surface shows `nida_masked` only (`••••••••1234`, `maskNIDA` in `phase3_dto.go`), so the number is effectively write-only. Separately, platform admin has org-level views (`/admin/orgs`) but no way to answer "who is this phone number / e-mail on our platform", which support needs first when a renter or landlord calls. Both must keep SPEC's rule: NIDA encrypted at rest, masked by default, every full view **audited**.

### 19.0 Docs first
- [x] SPEC §8 (security): "masked in UI" becomes "masked by default; full value on an explicit, audited reveal by the org's owner/manager or platform admin". SPEC §5.10 adds the user directory endpoints. API.md "Phase 19" section. FLOWS 3.2 note ("Reveal NIDA" on the request/renter page). DECISIONS rows: reveal is a separate call not a query flag (so the audit row is unmissable); no reveal of the KYC **document** beyond what exists (`kyc.view` already audited).

### 19.1 NIDA reveal (landlord)
- [x] `POST /renters/{user_id}/nida/reveal` (org audience, `org_owner` + `org_manager`) → `{nida_number, full_name, revealed_at}`. Renter must have a **live relationship with the org** (a link request in any status, or any contract, in this org) — otherwise 404 like the rest of the renter directory. Rate-limited per org (60/h) to make bulk scraping loud. `POST` not `GET`: never cached, never in a URL, never prefetched.
- [x] Audit `renter.nida_reveal` (entity user, org scope, actor, `after.reason` optional free text ≤200 chars from the request body). Shown in the org audit log and in the **renter's own** activity ("Your landlord viewed your NIDA number on {date}") via `GET /me/profile` `nida_reveals[]` (last 10) — the renter can see who looked, which is the deterrent SPEC intends.
- [x] Tenant app: renter detail and link-request detail keep the masked value; a **"Show full number"** button opens a confirm sheet (why you need it, one line, optional reason) → reveal → number shown in full for 60 s with a copy button, then re-masked. Never persisted client-side. SW/EN strings.
- [x] Enduser app: Profile → "Who has seen your NIDA" list from `nida_reveals`. SW/EN strings.
- [x] Tests: manager and owner can reveal, renter with no relationship → 404, cross-org → 404, audit row written with actor, limiter 429, `GET /me/profile` shows the reveal, the masked field on every existing endpoint is unchanged.

### 19.2 Platform user directory (admin)
- [x] `GET /admin/users?q=&kind=&status=&org_id=&cursor=&limit=` — cross-org, paged. `q` matches phone (normalised), e-mail (lower), full name (ILIKE prefix). Row: `id, kind, full_name, phone, email, status, created_at, last_seen_at?` plus per kind: renter → `orgs[]` (org name + relationship: `renting | applied | past`), `contracts_live`, `kyc_status`; org_user → `orgs[]` with `role`; platform_admin → nothing extra. **No NIDA, masked or full**, on the list.
- [x] `GET /admin/users/{id}` — everything above plus renter: `nida_masked`, link requests (last 10), contracts (all, with org/unit/status), payments summary (count, total, last paid_at); org_user: memberships and the org's status; every audit row where this user is the **actor or the entity** (last 50, `?cursor=` for more, reusing `handleAdminAuditLog`).
- [x] `POST /admin/users/{id}/nida/reveal` — same contract as 19.1, audit `renter.nida_reveal` with `actor_kind=platform_admin`, visible to the renter the same way. Support cases only; no bulk.
- [x] `POST /admin/users/{id}/suspend` / `activate` (`users.status`) with reason — sessions revoked on suspend (same pattern as org suspend). Out of scope: editing any user field, resetting PINs/passwords, deleting users.
- [x] Audit: `admin.user_view` (detail page opens — yes, reads are audited here because the page aggregates cross-org PII), `admin.user_suspend`, `admin.user_activate`. Admin audit filter kinds updated.
- [x] Admin app: `users` page (search box, kind/status filters, table, "Show older"); `users/[id]` page (header with status + suspend/activate, tabs: Overview · Tenancies · Payments · Activity; "Reveal NIDA" behind the same confirm sheet as 19.1). Link from the org detail page ("Members" and "Renters" lists → user detail). SW/EN strings.
- [x] Tests: search by each of phone/e-mail/name, kind and status filters, paging, cross-org rows visible to admin, org user cannot reach `/admin/users` (403), detail aggregates the right contracts/payments, suspend revokes sessions, audit rows for view/suspend/activate/reveal.

### 19.3 Name corrections (typos)
What exists: a renter can already fix their own name any time via `PUT /me/profile` (writes `renter_profiles.full_name` **and** `users.full_name`, audited `profile.update`); an owner can rename the **org** via `PATCH /org {name}`. Nobody can fix an **org user's** name (`PATCH /org/members/me` is locale-only; the name typed on the invite is final), a landlord cannot fix a renter's name they mistyped on import, and platform admin can fix nothing. Contract rule to keep: the rendered terms are a **snapshot** (`{{renter_name}}` frozen at creation, hash-covered), so a rename never rewrites a signed document — only the live `parties` header on list/detail views follows the account.
- [x] `PATCH /org/members/me {full_name}` — any org user fixes their own display name (2–80 chars, trimmed). Audit `member.update` (before/after). The name shows on staff lists, audit rows and the landlord signature block of contracts activated **after** the change.
- [x] `PATCH /org/members/{id} {full_name, role?}` — `org_owner` only, for a manager's typo or a role change; cannot target self for role, cannot demote the last owner (409 `last_owner`). Audit `member.update`.
- [x] `PATCH /renters/{user_id} {full_name}` (org audience, owner + manager) — landlord corrects a renter's name **only while the renter has no signed contract with any org** (`pending_signature` or no contract): typos usually come from CSV import or in-person onboarding, before signing. After the renter has signed anywhere → 409 `renter_signed` with the hint "ask the renter to fix it in their Profile". Writes both `users.full_name` and `renter_profiles.full_name`; audit `renter.update` with before/after; renter SMS'd "Your name on {org} was corrected to {name}" (`name_corrected` kind, credits apply) so a bad edit is noticed.
- [x] `PATCH /admin/users/{id} {full_name}` — platform admin, any user kind, no signed-contract restriction (support escalations), reason required (≤200 chars). Audit `admin.user_update` before/after/reason. Renter or org user notified the same way (SMS for renters, e-mail for org users).
- [x] Contract document: no change to the snapshot. The list/detail `parties.renter.name` and `parties.landlord.name` keep reading the live account; the document page and `/verify` keep the frozen text. DECISIONS row stating this.
- [x] Tenant app: Settings → Staff row "Edit" (owner) and own-name field in Profile/Settings; renter detail "Edit name" (disabled with the `renter_signed` hint once signed). Admin app: user detail "Edit name" with reason. Enduser: unchanged (Profile already edits the name). SW/EN strings.
- [x] Tests: member self-rename; owner renames manager; manager cannot rename others (403); last-owner demotion 409; renter rename before signing OK and SMS queued, after signing 409; admin rename any kind with reason; signed contract document + hash unchanged after every rename; audit before/after on each.

## Phase 20 — Field feedback: proof uploads, landlord logo, payment backfill — ✅ done 20 Sep 2026

Why: four things landlords and renters hit in the first weeks of real use. Each is small on its own; grouped so they ship together.

### 20.1 Proof of payment: PDF upload and locked amount
What exists: `application/pdf` is already accepted end to end — `proofContentTypes` (backend, 5 MiB), `PROOF_TYPES` (enduser) and the landlord viewer renders PDFs in an iframe. The block is the file input itself: `<input type="file" accept=… capture="environment">` in `ProofSheet.tsx` — on Android/iOS `capture` opens the **camera straight away**, so a renter with a bank PDF or a saved screenshot never sees a file picker. The amount box is prefilled with the row's outstanding balance but editable, so a typo makes the landlord confirm a proof for an amount that matches nothing.
- [x] Enduser `ProofSheet`: replace the single input with two actions — **Take photo** (`accept="image/*" capture="environment"`) and **Choose a file** (`accept="image/jpeg,image/png,application/pdf"`, no `capture`). Same `pickFile` path, same 5 MiB / type checks, PDF shows the file icon + name instead of a preview (already does). SW/EN strings (`proof.takePhoto`, `proof.chooseFile`).
- [x] Enduser `ProofSheet`: **amount read-only** when the sheet is opened from a schedule row (`target.scheduleId` set): rendered as a static `Money` line "Amount: TZS X (this period's balance)", no input, value sent as-is. The sheet opened from the generic "Send proof" button with no row (no `scheduleId`) keeps the editable box, because then there is nothing to lock it to. `proof.amount` label stays; add `proof.amountLocked` hint.
- [x] Backend `POST /me/proofs`: when `schedule_id` is given, `amount` must equal that row's outstanding at submit time → else **422 `amount_mismatch` {expected}**; the client re-reads and re-submits. Without `schedule_id`, unchanged (1 ≤ amount ≤ contract balance). API.md updated; test both branches.
- [x] Tenant proof review: nothing changes; the "amount" column already comes from the proof row.

### 20.2 Landlord logo in the app shell
What exists: `OrgMark` in `Shell.tsx` (rail top-left on desktop, mobile bar) draws a **plain `--primary` square** next to the org name; the uploaded logo (`/org/branding` → `logo_url`, presigned, used by contract documents and the branding page) never reaches the shell. `loadAndApplyOrgTheme` already fetches `/org/branding` on shell mount for the colours, so the URL is in hand.
- [x] `lib/branding.ts`: keep `logo_url` (and `display_name`) from the same `/org/branding` load in the cached theme (`THEME_CACHE_KEY`) so the logo paints on first frame like the colours do; presigned URLs expire → cache the URL with its `expires_at`, re-fetch when stale or on 403 of the `<img>` (`onError` → refetch once → fall back to the square).
- [x] `OrgMark`: `<img src={logo_url} alt="" >` 28×28, `object-fit: contain`, rounded, on a white tile so dark logos survive a dark primary; the square stays as the fallback when there is no logo. Same component feeds the rail and the mobile bar. Branding page: after a logo upload/delete, refresh the shell (the page already reapplies the theme — extend the same call).
- [x] Enduser: no change here (renter header already applies the org theme; renter-side logo is FLOWS 2 step 2, already shipped on the unit landing page). Check only.

### 20.3 Historical payments backfill (existing landlords)
What exists: Phase 16 `payments` CSV import allocates rows against a contract's **existing** schedules in date order, and Phase 5 "Record payment" accepts any `paid_at` up to a day ahead (backdating is fine). The gap is schedules: a renter application (`POST /units/{code}/link`) refuses a `start_date` more than **7 days in the past** (`startBackstopDays`), so a landlord who joined TMS mid-tenancy gets a contract whose book begins "now", and there is **nothing to allocate old rent to** — the import errors with "exceeds contract balance", and Record payment settles the *current* period with last year's money. Open question 6 chose "not supported" for Phase 16; this phase decides it.
- [x] Decision (DECISIONS row): a tenancy that predates TMS is represented **truthfully** — the contract's `start_date` is the real move-in date, the schedule generator produces the past periods, and the landlord settles them with real (backdated) payments or marks them settled. No ledger-only "historical" rows: they would leave the rent book unable to say whether a period was ever paid.
- [x] Backend: **landlord-created** contracts may start in the past — `POST /contracts` (manual, Flow 3.6) and the `renters` import accept `start_date` down to **10 years back**, with `end_date` derived as today; a renter **application** keeps the 7-day backstop (the renter should not be able to draft a back-dated tenancy alone). Activation generates the full span; past-due rows come out `overdue`, which is the truth until 20.3's settlement runs.
- [x] Backend: `POST /contracts/{id}/backfill` (org audience, owner + manager) `{until: date, mode: "paid" | "waived", paid_at?: date, method?, reference?, note?}` — one call that closes every schedule row with `due_date ≤ until` that is still unpaid: `paid` records **one payment per row** for its outstanding (audited `payment.record` with `after.source = "backfill"`, `paid_at` defaulting to the row's `due_date`, "backfilled" pencil chip next to "imported"), `waived` marks the rows `waived` with the note (already a schedule status, used by termination). Rows already paid/partial are skipped and reported. 409 `contract_not_active`; 422 when `until` is in the future or before `start_date`. Response `{settled: n, skipped: n, total: amount}`. Ledger and reports treat backfilled payments as ordinary cash by default — the chip is for the eye, `source` is for the filter.
- [x] Backend: `GET /contracts/{id}/schedules` and the renter's `/me/schedules` gain `source` on each row's last payment so the chip renders on both sides; `GET /payments?source=backfill|import|manual` filter; CSV export column.
- [x] Tenant app: contract page **"Backfill history"** sheet (visible while the contract has any unpaid row with `due_date < today`): pick the date up to which rent is settled (default: the last full period before today), choose Paid (method, one paid-at for all, or "use each due date") or Waived (reason), preview "N periods · TZS X" → confirm. Also offered inline on the **Overdue** list ("This is old history? Backfill…"). Import page `payments` kind: preview error text points to Backfill when a row predates the first schedule. New contract form and renters import: date picker allows the past with a "this tenancy started before TMS — past periods will be generated" hint. SW/EN strings.
- [x] Enduser: backfilled rows show the same "backfilled" chip and the landlord-recorded stamp; no action for the renter. Notifications: backfill sends **no** per-payment SMS (would be N texts for old news) — one `backfill_done` SMS "Your rent book on {org} now shows history up to {date}" (credits apply, kind can be disabled in Notification settings).
- [x] Reports: collection rate and revenue by default **exclude** `source=backfill` payments from the *period they are recorded in* and count them in the period their `paid_at` falls in (that is what `paid_at` is for) — verify the report queries bucket by `paid_at`, not `created_at`; add a test with a backfilled year.
- [x] Tests: past `start_date` accepted for manual/import, refused for application; backfill paid/waived; skips paid rows; 409/422 branches; renter sees chips; single SMS; audit rows; report bucketing by `paid_at`; import row before first schedule gets the hint.

## Phase 21 — Field feedback round 2 + arrears after move-out (27 Sep 2026) — 🔨 in progress, branch `phase-21-rename-after-sign`

- [x] Landlord renames a renter **after signing** (client decision): `PATCH /renters/{user_id}` takes `reason` (required once signed with this org), SMS always sent after a signature, 409 `renter_signed_elsewhere` when signed with another org. Tenant sheet gains the reason box. DECISIONS row supersedes Phase 19.3's.
- [ ] Pagination: tenant list pages that load one page and drop the rest (payments, schedules, contracts, units, renters, link requests, notifications log, properties, new-contract pickers); paginate `GET /reports/payment-status`, `/reports/upcoming`, `/me/schedules`.
- [x] **Arrears after move-out** (priority 1 of the unhappy-path catalogue): payments and proofs accepted on `ended`/`terminated` contracts; `GET /arrears` (Payments → Former tenants); owner-only, reversible `write-off` with new schedule status `written_off` (migration 000022). Tests: payment on a terminated contract, overpay refused, write-off/undo, write-off refused on a running contract, isolation.
- [ ] Next (Phase 22, client answers 27 Sep: all landlord-configured, stipulated by contract): settle-up on termination (proration full|pro-rata, prepaid refund|forfeit, partial credit), deposits (amount, deductions, refund), single-period waive/discount, eviction stages with SW+EN letters, renter notice to leave, holdover alert. Depends on template policy fields — see Phase 22 templates.

## Phase 22 — Contract templates per unit, contract policies, contract changes (27 Sep 2026) — 🔨 in progress, branch `phase-22-templates`

Why: every approved application was written on the org's single default template; a landlord with shops and flats, or different blocks, needs different agreements, needs to choose one for a particular renter, and needs a way to change a contract after signing. The client's unhappy-path answers (27 Sep) put proration, prepaid refunds, deposits and notice periods in the contract, configured by the landlord.

### 22.1 Template assignment
- [x] Migration 000023: `units.contract_template_id`, `properties.contract_template_id`. Resolution picked → unit → property → default in `createContractTx`; `template_source` in the audit row.
- [x] `GET /units/{id}/template`, `POST /units/bulk-template`, `PUT /properties/{id}/template`; approve takes optional `{template_id}`; template list `usage`; delete guard 409 `template_in_use`. Tests: resolution order, approval uses resolved/picked, delete guard, cross-org.
- [x] Tenant UI: approve sheet shows/changes the template; property and unit settings; units bulk action; usage and delete (409 surfaced) on the templates list.

### 22.2 Contract policies on the template (client answers)
- [x] Backend (migration 000024, `contract/policy.go`, tests incl. hash stability and verify after template change). [x] Template editor UI, contract rules block, templates-list chip. — Structured `policy` on templates, snapshotted on each contract and covered by the hash: `move_out_proration` (full_month|pro_rata), `early_exit_prepaid` (refund|forfeit|landlord_decides), `deposit` (none|fixed amount|N months) + `deductions_may_exceed_deposit`, `tenant_notice_days`, `eviction_notice_days`. New variables `{{deposit}}`, `{{notice_days}}` for the text. Existing contracts keep an empty policy (hash unchanged).

### 22.3 Stale unsigned contracts
- [x] Backend: `content_updated_at` (migration 000025), `template_changed` on contracts, `stale_pending` on template save, `POST /contracts/{id}/reissue`, `POST /contract-templates/{id}/reissue-pending`; refused once anyone has signed. [ ] UI. — Editing a template flags its `pending_signature` contracts "wording changed — reissue?"; reissue = withdraw + fresh contract from the same parameters (Phase 17 `reissue`).

### 22.4 Contract changes (Phase 17 supersession)
- [ ] Amend / renew / reissue as drafted in Phase 17, including switching template as an amendment.

### 22.5 Settle-up on termination, deposits (reads 22.2 policy)
- [ ] Termination settlement, deposit record and refund, single-period waive/discount, eviction stages with SW+EN letters, renter notice, holdover alert.

## Open questions (answer whenever; defaults applied if unanswered)

1. **Credit unit**: 1 credit per 160-char GSM segment (default; 70 for UCS-2) vs 1 credit per message regardless of length. OTP/security messages exempt (default yes).
2. Expense **approval**: managers can record expenses; should owners approve/void only? Default: owner + manager record and void; audit shows who.
3. Revenue **basis toggle**: cash (collected) is the default; add an "accrual (expected)" toggle in Reports? Default: both lines always shown, no toggle.
4. Theme **per app**: one org theme applies to both landlord and renter apps (default).
5. Receipts as **PDF** allowed? Default: yes (image/jpeg, image/png, application/pdf, ≤5 MiB).
6. **Payments import for history before the contract start** (rent paid before the landlord joined TMS): default is *not supported* — the contract's `start_date` should be the real tenancy start so past periods exist as schedules; alternative is a ledger-only "historical" row that touches no schedule and shows only in History. Phase 16 ships the default.
7. **Proof of payment on submit**: SMS/e-mail to the landlord (costs credits) or in-app badge only (default)?
8. **Import undo window**: 24 h (default) vs until the next import.

## Risks

| Risk | Mitigation |
|------|------------|
| Theme freedom breaks legibility | Presets first; advanced override gated by contrast validator on both client and server; stamps fixed |
| Report math drift between endpoints | Single `internal/period` + `internal/report` aggregates; reconciliation tests against raw payments/expenses sums |
| Mobile refactor regresses desktop | Layout-level Shell with breakpoint switch; screenshot pass at 375/768/1280 in Phase 15 |
| Nav layout refactor touches 25 pages | Mechanical move in Phase 9 with a build-time check that no page imports `<Shell>` directly |
| i18n touches every screen | Dictionary completeness check in `make lint`; strings moved app by app (enduser first, smaller); Swahili copy reviewed by the client before UAT 2 |
| Credit debit races (worker pool N=3) | Single conditional UPDATE per send, `-race` test, ledger append-only with balance_after for reconciliation |
| Rent-per-period change alters printed figures | Only new contracts affected (snapshot rule); DECISIONS entry; JJnE re-issues the test contract |
