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

## Phase 15 — Mobile landlord pass, hardening, UAT 2 (1 day)

- [ ] **Mobile landlord (#5 full)**: every tenant screen audited at 375/414/768 px: properties, units board (card view), link inbox, renters, contracts + document (print unaffected), payments (record sheet full-screen), expenses, reports (charts scale, tables scroll), settings, branding, notifications (credits header, bulk compose SW/EN), audit. Bottom bar for the 5 most-used sections; sheets slide from bottom; sticky action bars respect safe areas. PWA install banner on tenant app. Renter app re-checked in both languages (Swahili strings are longer).
- [ ] Performance: reports v2 endpoints in `make loadtest` (p95 < 300 ms on seed org); indexes for `expenses (org_id, incurred_on)`, `(org_id, property_id, incurred_on)`, `payments (org_id, paid_at)`, `notifications (org_id, status) WHERE status='held_no_credit'`.
- [ ] Isolation census covers all new routes (build fails otherwise); rate limits on receipt uploads and admin template edits; CSV neutralisation on expenses export.
- [ ] Seed v2: `make seed` adds 12 months of expenses per property and payments spread over a year so trends are meaningful, renters split SW/EN, 500 credits per org; `make seed-demo` adds JJnE expenses, a custom theme, and an edited platform template.
- [ ] Docs: SPEC/FLOWS/API/README/DEV updated; `docs/UAT.md` Part 2 section (flows 9 v2, 12 expenses, 13 admin templates/credits, branding v2, language switch, mobile checklist).

**Exit:** UAT 2 checklist executed clean on the ngrok preview from a phone (landlord + renter, both languages); `make build/test/lint/test-isolation/loadtest` green; PROGRESS.md entries for 9–15.

---

## Open questions (answer whenever; defaults applied if unanswered)

1. **Credit unit**: 1 credit per 160-char GSM segment (default; 70 for UCS-2) vs 1 credit per message regardless of length. OTP/security messages exempt (default yes).
2. Expense **approval**: managers can record expenses; should owners approve/void only? Default: owner + manager record and void; audit shows who.
3. Revenue **basis toggle**: cash (collected) is the default; add an "accrual (expected)" toggle in Reports? Default: both lines always shown, no toggle.
4. Theme **per app**: one org theme applies to both landlord and renter apps (default).
5. Receipts as **PDF** allowed? Default: yes (image/jpeg, image/png, application/pdf, ≤5 MiB).

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
