# PLAN2.md — TMS Part 2 (post-MVP iteration)

Continues [PLAN.md](PLAN.md) (Phases 0–8, done 5 Sep 2026). Same rules: backend-first, real endpoints only, audit + org-scope on every mutation, Make + Git only, one feature branch per phase (`phase-N-name`), `PROGRESS.md` entry per phase, `DECISIONS.md` for spec-silent calls. SPEC.md / FLOWS.md / API.md are amended **before** code in each phase (TECHSTACK rule).

## Scope (client requests, 5 Sep 2026)

| # | Request | Phase |
|---|---------|-------|
| 1 | Time-series graphs + trends for revenue | 11 |
| 2 | Expense tracking and logging | 10 |
| 3 | Reports/trends with cadence selection: month, quarter, 6 months, annual, custom | 11 |
| 5 | Landlord app mobile-friendly | 9 (shell), 13 (full pass) |
| 6 | Landlord branding: choose app theme (background, accents, …) — presets + advanced override | 12 |
| 7 | Landlord left nav reloads on page change | 9 |

Decisions taken with the client:
- **Theme:** curated presets by default + an "advanced" panel exposing individual tokens with live WCAG-AA contrast warnings. Stamp colors (Paid/Overdue) stay fixed platform-wide.
- **Expenses:** property-level ledger (optional unit), receipts, categories; plus an all-properties summary.
- **Revenue series:** collected (cash basis, non-reversed) vs expected (schedules due) vs expenses → net; period-over-period % change.

---

## Phase 9 — Foundations for Part 2 (0.5 day)

- [ ] **SPEC amendments**: §2.0 theme model (presets + token override, contrast rule, stamps fixed); §4 new tables `expenses`, `expense_categories`, `org_themes`; §5.11 expenses API, §5.9 reports v2 (cadence param), §5.2 theme endpoints; §7 bucket `receipts`. FLOWS: flow 12 "Expenses", flow 9 updated (cadence + trends), flow 1 branding step updated. API.md "Part 2" section.
- [ ] **Fix #7 — nav remount**: move `<Shell>` from the 25 tenant pages into a route-group layout `apps/tenant/app/(portal)/layout.tsx` (auth pages stay outside). Shell state (pending badges, org branding, theme) fetched once and kept across navigation; badges refresh on focus/interval, not on route change. Same pattern applied to `apps/admin`.
- [ ] **Responsive shell (#5 base)**: tenant `Shell` gets a collapsible left nav → bottom bar / hamburger under 768px; tables get an `overflow-x` wrapper + card fallback for narrow widths; sheets become full-screen on mobile; touch targets ≥44px (already in tokens).
- [ ] **Shared `PeriodPicker`** component (`packages/ui`): cadence `month | quarter | half_year | year | custom` + navigation (prev/next), emits `{from, to, cadence}`; used by every report and dashboard card in Phase 11.
- [ ] Backend: `internal/period` package — resolves cadence + anchor date to `[from, to)` in EAT (reuse `internal/tz`), plus bucket size for series (`day` for ≤ 62 days, `week` ≤ 26 weeks, `month` otherwise, overridable).
- [ ] Migrations `000012_part2_foundations` (tables below, empty for now), buckets `receipts` in compose init.

**Exit:** navigating between tenant pages keeps the nav mounted (no refetch flash); tenant app usable at 375px on the existing screens; `PeriodPicker` renders in the design-system page; tests green.

## Phase 10 — Expenses (1 day)

Schema:
```
expense_categories  org_id, name, is_default, sort_order, active            -- seeded: Repairs & maintenance, Utilities, Security, Cleaning, Taxes & levies, Insurance, Management fees, Other
expenses            org_id, property_id, unit_id NULLABLE, category_id, amount (TZS), incurred_on (date),
                    vendor, reference, note, receipt_object_key NULLABLE, recorded_by_user_id,
                    status (recorded|voided), voided_at, void_reason, deleted_at
```
- [ ] API (`/expenses`, `/org/expense-categories`): CRUD categories; `POST /expenses` (validation: amount bounds, date ≤ today+1, property in org, unit belongs to property), `PATCH` (same day edits allowed; audited before/after), `POST /expenses/{id}/void {reason}` (append-style correction, like payment reverse), `GET /expenses?property_id=&unit_id=&category_id=&from=&to=&cursor=` + `format=csv`, receipt presigned upload/complete/view (bucket `receipts`, `{org_id}/{expense_id}.{ext}`, ≤5 MiB, image/pdf), `GET /expenses/summary?cadence=&from=&to=&group_by=property|category` → totals per group + grand total (all-properties view).
- [ ] Tenant screens: **Expenses** nav item → ledger (PeriodPicker, property/category filters, totals with double rule, CSV export), "Record expense" sheet (with receipt upload), expense detail (receipt viewer, void with reason), per-property tab on the property page, settings → expense categories manager.
- [ ] Reports hook: expenses feed Phase 11 net figures.
- [ ] Tests: validation, void restores nothing (append semantics), CSV formula-neutralised, isolation (org B → 404 on every expense route; census table extended — build fails otherwise), summary math by property/category.

**Exit:** landlord records an expense with receipt on Mbezi Beach Block A, sees it in the property tab and in the all-properties summary; void audited; CSV downloads.

## Phase 11 — Reports v2: cadence + time-series + trends (1 day)

- [ ] **Cadence everywhere (#3)**: `cadence=month|quarter|half_year|year|custom&from&to&anchor` accepted by `/reports/summary`, `/reports/payment-status`, `/reports/collections`, `/expenses/summary`; response echoes the resolved `{from,to,cadence}` and a `previous` window for comparison.
- [ ] **Revenue series (#1)**: `GET /reports/revenue?cadence=&from=&to=&bucket=&property_id=` → `{buckets:[{start,expected,collected,expenses,net}], totals, previous_totals, change_pct:{collected,expenses,net}, trend:{slope_collected_per_bucket}}`. Cash basis (non-reversed payments by `paid_at`), expected = schedules due in bucket (excl. waived), expenses by `incurred_on` (excl. voided). Zero-filled buckets; ≤ 400 buckets.
- [ ] **Per-property breakdown**: `group_by=property` variant for revenue + expenses; occupancy series `GET /reports/occupancy?cadence…` (units occupied per bucket end).
- [ ] Tenant **Reports** rework: Overview (period tiles + Δ vs previous period), **Revenue** tab (line/area chart collected vs expected, bars expenses, net line; hover tooltips; per-property toggle), Expenses tab (by category donut → keep simple stacked bars + table), Payment status, Collections; every tab driven by the shared `PeriodPicker`; charts as inline SVG in `packages/ui/charts` (no chart lib; follow the dataviz skill for color/legend/axis rules; dark/light aware).
- [ ] Dashboard cards: "Revenue this period" sparkline + Δ%, "Net income", "Expenses" card added to `dashboard_prefs.cards`.
- [ ] Tests: period resolver (month/quarter/half-year/year boundaries in EAT, custom validation, max range 5 years), bucket auto-sizing, revenue math on seeded org (reconcile totals with `/payments` and `/expenses` sums), previous-period comparison, per-property sums equal grand total.

**Exit:** Reports show revenue vs expected vs expenses vs net over any cadence incl. custom range, with % change vs previous period; per-property breakdown; seed data produces a visibly correct chart.

## Phase 12 — Theming v2: presets + advanced override (1 day)

- [ ] **Token model** (`packages/ui`): expose the themable set — `paper` (background), `surface` (sheets/cards), `ink` (text), `ink-muted`, `rule` (ledger lines), `primary` (+ derived pressed/tint/on-primary), `accent`, `font`. Fixed forever: stamp colors, focus ring, spacing, radii, type scale.
- [ ] **Presets** (8): Ledger (current default), Night ledger (dark), Warm paper, Cool slate, Forest, Ocean, High-contrast, Minimal white — each a full token set validated for WCAG AA.
- [ ] **Contrast guard**: `packages/ui/theme` `validateTheme(tokens)` → list of failing pairs (text/paper, muted/paper, on-primary/primary, ink/surface) with ratios; backend re-validates on save and rejects < 4.5:1 for body text (400 with the failing pairs).
- [ ] API: `GET/PUT /org/branding` gains `theme: {preset_id|null, tokens:{...}|null, font_id}`; `/public/orgs/{slug}/branding` and `/public/units/{code}` return the resolved token set; `GET /themes/presets` (public) lists presets.
- [ ] Tenant **Branding** screen: preset gallery with live preview (renders a mini ledger + stamp + button with the candidate theme), "Advanced" panel (color inputs per token, live contrast badges, reset to preset), applies immediately to the tenant app; enduser app applies the same resolved theme on org pages (QR landing, connect, contract, rent book).
- [ ] `applyOrgTheme()` v2 sets all tokens; falls back to preset "Ledger" when the org has none; admin app never themed.
- [ ] Tests: validator table (passing/failing pairs), backend rejection, presets all pass AA, public endpoints resolve tokens; visual check of each preset on the design-system page in light + dark system settings.

**Exit:** JJnE picks "Night ledger", tweaks accent in advanced, saves; tenant app + renter QR landing reflect it; a low-contrast choice is blocked with a clear message.

## Phase 13 — Mobile landlord pass, hardening, UAT 2 (1 day)

- [ ] **Mobile landlord (#5 full)**: every tenant screen audited at 375/414/768 px: properties, units board (card view), link inbox, renters, contracts + document (print unaffected), payments (record sheet full-screen), expenses, reports (charts scale, tables scroll), settings, branding, audit. Bottom bar for the 5 most-used sections; sheets slide from bottom; sticky action bars respect safe areas. PWA install banner on tenant app.
- [ ] Performance: reports v2 endpoints in `make loadtest` (p95 < 300 ms on seed org); indexes for `expenses (org_id, incurred_on)`, `(org_id, property_id, incurred_on)`, `payments (org_id, paid_at)`.
- [ ] Isolation census covers all new routes (build fails otherwise); rate limits on receipt uploads; CSV neutralisation on expenses export.
- [ ] Seed v2: `make seed` adds 12 months of expenses per property and payments spread over a year so trends are meaningful; `make seed-demo` adds JJnE expenses + a custom theme.
- [ ] Docs: SPEC/FLOWS/API/README/DEV updated; `docs/UAT.md` Part 2 section (flows 9 v2, 12 expenses, branding v2, mobile checklist).

**Exit:** UAT 2 checklist executed clean on the ngrok preview from a phone (landlord + renter); `make build/test/lint/test-isolation/loadtest` green; PROGRESS.md entries for 9–13.

---

## Open questions (answer whenever; defaults applied if unanswered)

1. Expense **approval**: managers can record expenses; should owners approve/void only? Default: owner + manager record and void; audit shows who.
2. Revenue **basis toggle**: cash (collected) is the default; add an "accrual (expected)" toggle in Reports? Default: both lines always shown, no toggle.
3. Theme **per app**: one org theme applies to both landlord and renter apps. Should the renter app be allowed a separate theme? Default: one theme, both apps.
4. Receipts as **PDF** allowed? Default: yes (image/jpeg, image/png, application/pdf, ≤5 MiB).

## Risks

| Risk | Mitigation |
|------|------------|
| Theme freedom breaks legibility | Presets first; advanced override gated by contrast validator on both client and server; stamps fixed |
| Report math drift between endpoints | Single `internal/period` + `internal/report` aggregates; reconciliation tests against raw payments/expenses sums |
| Mobile refactor regresses desktop | Layout-level Shell with breakpoint switch; screenshot pass at 375/768/1280 in Phase 13 |
| Nav layout refactor touches 25 pages | Mechanical move in Phase 9 with a build-time check that no page imports `<Shell>` directly |
