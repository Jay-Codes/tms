# PROGRESS.md — phase log (resume point for orchestration sessions)

## 2026-09-05 — Phase 0: Foundations & infrastructure ✅

**Shipped**
- Compose infra up: postgres:17 (host port **5433**), redis:7, minio (+ buckets `branding`, `qrcodes`, `kyc`, `signatures`).
- `backend/` Go module (`tms/backend`): chi router at `/api/v1`, slog, RFC-7807 problems, config from env, pgx pool, golang-migrate embedded, sqlc scaffold, go-redis, MinIO client + presign helpers, `notify.SMSProvider` with dev `LogProvider` (OTP visible in `.dev/api.log`).
- `GET /api/v1/healthz` → `{"status","db","redis","minio"}`; verified 200 all-ok through proxy :8080.
- Proxy routes `/api/*` → :8081.
- Make targets: `api`, `api-stop`, `api-restart`, `api-log`, `proxy-restart`, `migrate`, `migrate-down`, `sqlc`, `build`, `test`, `lint`.
- `make test` green (config, httpserver, notify). `make lint` falls back to `go vet` (golangci-lint not installed).

**Deferred / notes**
- ngrok tunnel was already stopped before this session; restart blocked by tool permissions. Public URL unverified; local `http://localhost:8080` verified. Run `make preview` (or restart ngrok) and update `APP_BASE_URL` in `.env`.
- Frontend apps have no `build`/`lint`/`test` npm scripts yet — `make build/lint/test` cover Go only until Phase 1 frontend lanes add them.

**Decisions:** see DECISIONS.md (branch rename, `qrcodes` bucket, Postgres :5433, SMS provider selection, migrate-as-library).

## 2026-09-05 — Phase 1: Schema, auth, org onboarding ✅

**Shipped**
- Migration `000002_schema`: all 18 SPEC §4 tables (TEXT+CHECK enums, updated_at trigger, soft delete, org_id index everywhere, pgcrypto NIDA column, audit_log append-only via RAISE triggers). Payment periods + branding seeded on org create. Platform admin seeded from `ADMIN_EMAIL/ADMIN_PASSWORD` (dev: admin@tms.local / admin12345).
- Auth: OTP send/verify (Redis, rate-limited, dev code in `.dev/api.log`), renter register (argon2id PIN), email/password login, per-audience cookies (`tms_r`/`tms_o`/`tms_a`), Redis+Postgres sessions, RBAC middleware, email verification + staff invites (links logged), `POST /orgs`, `GET/PATCH /org`, member CRUD, `GET /audit-log` (+ `/{id}`). Trusted-proxy client IP; prod refuses dev log providers.
- Frontend tenant: login, signup, verify, invite, dashboard shell, 6-step wizard shell, settings (org + members), audit page. Enduser: register (3 steps), login (PIN/OTP), home, profile, bottom bar. Shared `lib/api.ts` + `lib/auth.tsx` conventions in each app. `build`/`lint` scripts on all three apps (`NEXT_DIST_DIR=.next-build`).
- Tests: unit + Postgres-backed integration (`make test-db`), org-scope SQL guard, migration guard, security tests (audit payload secrets, append-only), isolation tests (cross-org 404).
- Verified in preview: org signup → wizard → settings; renter register with OTP from log → home; curl isolation (org B → org A member/audit = 404).

**Deferred:** wizard step bodies (later phases); email verification not enforced for login (banner only). Sec review findings all fixed (H1, M1–M4, L1–L6).

## 2026-09-05 — Phase 2: Properties, units, QR, pricing, vacancy ✅

**Shipped**
- Backend: migration `000003_phase2`; payment periods CRUD + restore-recommended; properties CRUD (unit_counts, cascade soft-delete, 409 on live contracts); units CRUD/bulk, status override rules, vacancy board filters + cursor; price plans (history, current by effective_from, atomic bulk percent/set, bounds); QR PNG → MinIO `qrcodes` (presigned via public host through proxy bucket routes); public branding + unit endpoints (prorated periods, rate-limited). 16-route cross-org isolation table.
- Tenant app: properties list/detail, add/bulk-add units, QR print sheet (A4 print CSS), unit page (rename, override, allowed periods, QR, price history/new price), vacancy board (tabs, property filter, search, days vacant, bulk price), payment periods manager (settings + wizard), wizard steps First property / Payment periods / Units.
- Verified live: property + units created, QR sheet with real PNGs served through :8080, price change + bulk +10%, period add/reorder, public unit resolves branding + prorated amounts.

**Deferred:** bulk unit create non-atomic (spec silent); `unit_ids` cap 200. Presigned URLs use `MINIO_PUBLIC_URL` (=localhost:8080 until ngrok is restarted).

## 2026-09-05 — Phase 3: Renter onboarding, KYC, linking ✅

**Shipped**
- Backend: migration `000004_link_requests`; `internal/contract/schedule.go` generator (table-tested: 180/45, 100/30, 7d, due_day clamps); renter profile + NIDA (encrypted, masked, never audited), KYC presigned upload/complete/view; link requests (create rules, cancel, inbox, approve/reject, auto-approve); renter directory; `notification_log` queue + Redis worker + SW/EN templates (`link_approved`, `link_rejected`).
- Enduser: `/u/{code}` branded landing (applyOrgTheme), KYC form + ID upload, connect flow (period, term, start date, preview), request ledger on home. Tenant: link-request inbox + detail (KYC panel, approve/reject), renter directory + detail, nav badge, dashboard card.
- Verified live: renter connect via UI → landlord inbox → approve → Swahili SMS in `.dev/api.log`, notification_log `sent`; auto-approve path via curl (100d/30d → 4 rows, last 83,333).

**Deferred to later phases:** worker atomic claim for multi-worker pool (Phase 6); orphaned KYC objects on re-upload; `GET /renters` N+1 (Phase 8 load pass); enduser `KycStatus` type lacks `rejected`.

## 2026-09-05 — Phase 4: Contracts & schedules ✅

**Shipped**
- Backend: migrations `000005_contracts`, `000006_signatures_append_only`; templates CRUD + bluemonday sanitizer + preview + default seed; contracts (create, link-approval hook + backfill, list/get/document, OTP sign + drawn signature, activate w/ schedules + landlord_recorded path, terminate, verify, schedules), lifecycle job (hourly + `/admin/jobs/contract-lifecycle`), branding endpoints (logo/letterhead/footer/theme). Sign OTP verify capped, hash rechecked at activate, signature object stat-checked.
- Tenant: contracts list/detail (document, Activate/on-behalf/Terminate, schedules), TipTap template editor w/ variables + snapshot banner + letterhead preview, branding settings, wizard Branding + Contract template steps. Enduser: contract list, document view + print CSS, OTP + canvas signature flow, home next-payment + sign prompts.
- Verified live: Room 3 contract backfilled → renter signed in UI (OTP from log + drawn PNG) → landlord activated in UI → 4 schedules (250k×3 + 83,333), unit occupied, welcome SMS, `/verify` valid; letterhead + footer render in document; template edit/preview; new contract + terminate frees unit.

**Note:** Opus session limit hit mid-phase (resets 3pm); lanes relaunched and resumed from partial work.

## 2026-09-05 — Phase 5: Offline payments & statuses ✅

**Shipped**
- Backend: migration `000007_payments` (+ `payment_allocations`); pure allocator (paid/partial/rollover-confirm/exceeds-balance/schedule_paid); `POST /payments`, reverse, lists, `GET /schedules` board, bank account settings, `/me/schedules` (+next_due, overdue_total, bank_account, days_overdue), `/me/payments`; overdue sweep (hourly, on-demand per org before reads, admin job); thank_you SMS (SW/EN, next due). FOR UPDATE locking; concurrency test proves no double-settle.
- Tenant: Payments page (Overdue / Due soon / Partial / All / History), Record payment sheet with overpay confirm, Reverse sheet, contract + renter payment sections, bank account settings, dashboard cards. Enduser: Payments tab (next due, overdue banner, how-to-pay bank sheet with copy, grouped ledger, history with reversed stamp).
- Verified live: UI record (Room 3 → PAID, thank-you SMS w/ next due in log), overpay confirm → rollover across two rows, reversal restores, bank account round-trip, renter ledger stamps/pencils.

**Deferred:** overdue red-stamp path not exercised with real aged data in UI (backend tests cover the sweep); Phase 8 seed will backdate rows for UAT.

## 2026-09-05 — Phase 6: Notifications end-to-end ✅ (live Beem deferred)

**Shipped**
- Backend: migration `000008_notifications`; unified `notify.Render` for all 11 kinds (SW/EN, org overrides, whitelist); scheduler (5-min ticker, EAT local date, send-hour gate, reminder_7d / reminder_due / overdue_daily / unsigned_reminder with dedupe keys, `/admin/jobs/notifications`); worker pool N=3 with atomic claim + backoff + stale-`sending` sweep; real Beem HTTP client (httptest-covered); notification settings, custom bulk SMS (rate-limited, audited), log + retry.
- Tenant: `/settings/notifications` (sender, language, hour, per-kind toggles, template overrides w/ chips), `/notifications` (Log + Send message composer w/ preview + confirm), wizard Notifications step, renter Messages section.
- Verified live: settings save + 400 on unknown variable; custom SMS → log SENT with sender `JJNE`; admin job with date overrides queued reminder_7d / reminder_due (dedupe on re-run).

**Deferred:** live Beem smoke (no credentials — set `BEEM_API_KEY/BEEM_SECRET_KEY/BEEM_SENDER_ID` in `.env`, `make api-restart`, send a custom message). Queued messages of a later-suspended org still send (small window).

## 2026-09-05 — Phase 7: Reports, branding, admin app, PWA ✅

**Shipped**
- Backend: migration `000009_phase7`; reports (summary, payment-status json+CSV w/ formula neutralisation, collections buckets), dashboard_prefs validation, platform admin (orgs list/detail, suspend/activate w/ Redis flag + DB fallback, metrics, cross-org audit search, jobs); suspension enforced on org routes, public 404, scheduler/claim skip.
- Tenant: Reports (Overview / Payment status + CSV / Collections SVG chart), dashboard cards ordered by prefs + Customize sheet, audit filters + diff expander, PWA (manifest, sw.js, icons). Admin app: login, metrics, orgs, suspend/activate, audit search, jobs runner, PWA. Enduser: PWA + install banner + org theme sync on protected pages.
- Verified live: reports numbers for JJnE (2 props, 7 units, 33% occupancy), CSV download, dashboard customize persisted, admin suspended/activated Org B, audit search shows both events.

**Carried to Phase 8:** `units`/`renters` `q` ILIKE wildcard escaping; global `audit_log(at,id)` index; shared tz package; `PUT /org/branding` dashboard_prefs replace semantics doc.

## 2026-09-05 — Phase 8: Hardening & UAT prep ✅

**Shipped**
- Isolation census: chi.Walk over all 110 routes; org B / renter 2 / non-admin runs; new routes fail the build until covered. Rate-limit pass (11 endpoints, 3 new limits), validation sweep (159 probes, no 5xx), race run clean, `internal/tz`, migrations 000010 (audit index) + 000011 (contract list indexes).
- Full-stack compose profile: Dockerfiles (api 38MB, proxy 24MB, apps ~320MB), `make images/deploy/deploy-down/logs`, proxy env upstreams + TLS + `make tls-selfsigned`, `MIGRATE_ON_START`; verified on :8090 incl. presigned QR through the proxy.
- Seed (`make seed`), demo seed (`make seed-demo`), `make loadtest` (all p95 < 60 ms after batching contract signatures; pool `DB_MAX_CONNS`), `docs/UAT.md`, `docs/LOADTEST.md`, README/DEV updated.

**Open items for the user**
- Restart ngrok (`make preview` or ngrok alone) and set `APP_BASE_URL` + `MINIO_PUBLIC_URL` in `.env`, then `make api-restart`, so QR scan URLs and presigned images resolve from phones.
- Supply Beem creds for the live SMS smoke (PLAN Phase 6 item).
- golangci-lint not installed locally (`brew install golangci-lint`); `make lint` falls back to `go vet`.

## Phase 9 — Foundations + end-to-end fixes (5 Sep 2026) — branch `phase-9-foundations`

**Shipped**
- Docs first: SPEC §2.0/§3.2/§4/§5.1–5.13/§6/§7/§10, FLOWS 1/2/5/6/8/9 + new 12 (Expenses) and 13 (Admin templates & credits), API.md "Part 2 planned contract", 9 DECISIONS rows.
- Nav remount fix: `<Shell>` mounted once in `apps/tenant/app/(portal)/layout.tsx` (and admin); 29 pages lost their wrapper; `useNavBadges` refreshes on focus + 60 s; ESLint `no-restricted-imports` guard forbids `Shell` in pages.
- Responsive tenant shell: hamburger drawer + fixed bottom bar under 768 px, `TableScroll` wrapper on main tables, full-screen sheets on mobile; desktop unchanged.
- `PeriodPicker` + pure `period.ts` helpers in `packages/ui` (9 node tests), demo on `/tenant/design-system`.
- Backend `internal/period` resolver (month/quarter/half_year/year/custom, EAT, previous window, bucket sizing, ≤400 buckets) with table tests.
- Single recommended period: migration 000012 data fix + partial unique index, `POST /org/payment-periods/{id}/recommend` (audited `payment_period.recommend`), bootstrap/seed/restore only badge Monthly, PATCH rejects `is_recommended`; UI "Set as recommended".
- Rent per payment period: `contract.RentPerPeriod`, `{{rent}}` scaled, new `{{rent_basis}}`, `rent_per_period` on contract DTOs; default template sentence updated; renter document shows "TZS 300,000 / Quarterly (90 days)" + basis line.
- Renter contract overflow fixed (app-scoped `enduser.css` + document.css: `.ledger-kv`, wrapping `.num` inside docs, nowrap dates); verified 320/375/414 with zero overflowing elements.
- Migration 000012 also adds `users.locale`, empty `expense_categories`, `expenses`, `org_themes`, `platform_templates(+locked)`, `platform_template_versions`, `org_sms_credits`, `sms_credit_ledger` (append-only), `held_no_credit` status; `receipts` bucket in compose + proxy route.
- `make build` now uses `NEXT_DIST_DIR=.next-build`; new `make apps-restart`.

**Verified**: `make build/test/lint` green; curl: one recommended per org, recommend round-trip, audit rows; browser: tenant 375 px shell + 1280 periods page, renter contract at 375 px, no page horizontal scroll.

**Incident**: `make build` (plain `next build`) overwrote the three dev servers' `.next` → 404 chunks / 500s. Fixed the Makefile and restarted only the Next.js dev servers via the new `make apps-restart` (proxy, ngrok, api untouched). Dev servers' pids in `.dev/*.pid` changed.

**Deferred**: `setup` page lives inside `(portal)`; `design-system` stays outside (unauthenticated reference page). `.stamp` rotation bleeds ~2 px on the renter contract header (cosmetic, packages/ui, Phase 15 pass).

## Phase 10 — Expenses (5 Sep 2026) — branch `phase-10-expenses`

**Shipped**
- Backend: `internal/expense` (default categories, seeding, group math, `ChangePct`), 18 sqlc queries, handlers for categories CRUD (lazy seeding of 8 defaults for existing orgs, bootstrap + seed for new ones), expenses create/patch/void/list/get, CSV (formula-neutralised, cap 10 000), receipts presign/complete/view/delete in bucket `receipts` (rate-limited 30/h), `GET /expenses/summary` by property/category with previous window + `change_pct`; migration 000013 adds `receipt_content_type`/`receipt_size`. 8 audit actions. Isolation census 125/125; org-scope guard extended to expense tables.
- Tenant: `/expenses` ledger (PeriodPicker persisted, filters, summary strip with category/property toggle, totals double rule, cursor paging, CSV export), `ExpenseSheet` (record/edit + receipt create→presign→PUT→complete), `/expenses/[id]` (receipt viewer, edit, void with reason, VOIDED stamp), property page Expenses section, `/settings/expense-categories` manager, dashboard "Expenses this month" card (opt-in; card id `expenses` added to the backend allowlist), nav + settings links.

**Verified**: `make build/test/lint` green; browser: record with PNG receipt via proxy, property tab, all-properties summary, void, CSV (`text/csv`, attachment filename), 375/1280 px. API.md "Part 2 — Phase 10 (shipped)" section added.

**Notes**: `receipt/complete` requires `{object_key}` (frontend echoes the ticket's key). Summary `previous` carries `cadence`; `Uncategorised` group appears only when non-empty. Three test expenses remain in JJnE's September ledger (dev data).

## Phase 11 — Reports v2: cadence + time-series + trends (5 Sep 2026) — branch `phase-11-reports-v2`

**Shipped**
- Backend: cadence (`month|quarter|half_year|year|custom`, `anchor`, `from`, `to`) on summary / payment-status / collections / expenses-summary with `window`, `previous`, `previous_totals`, `change_pct`; legacy `period=YYYY-MM` still accepted. `GET /reports/revenue` (day-grain SQL, Go bucketing day/week/month, zero-filled, ≤400 buckets → 422 `too_many_buckets`, `trend.slope_collected_per_bucket`, `collection_rate`, `group_by=property`), `GET /reports/occupancy` (per-bucket-end occupancy, terminated contracts through effective date). `internal/report/trend.go` (ChangePct/Slope/Rate). Migration 000014 report indexes. Loadtest routes added. Isolation census green. Seed org `cadence=year` answers in 3–8 ms.
- Tenant: `packages/ui/src/charts` (LineAreaChart, BarChart, Sparkline, StatTile/ChangeMark/RowBar, Legend, ChartFrame, axis/scale/format helpers; dataviz palette tokens `--chart-1…8` + role aliases), Reports page rewritten with one PeriodPicker + property filter and six tabs (Overview, Revenue, Expenses, Occupancy, Payment status, Collections), dashboard cards `revenue` (sparkline) and `net_income` (allowlisted server-side), charts demo on the design-system page.

**Verified**: `make build/test/lint` green; browser on live data: Overview tiles (collected 1,000,000 / expected 1,590,000 / expenses 137,000 / net 863,000, collection rate 63%, occupancy 46%), Revenue tab line/area chart with by-property toggle.

**Notes**: `occupancy_pct` is 0–100, `collection_rate` 0–1. Unknown cadence spelling → 400; unsatisfiable window → 422. Chart dark steps apply under `[data-theme="dark"]` only (paper/ink still light-only until Phase 12).

## Phase 12 — Theming v2: presets + advanced override (5 Sep 2026) — branch `phase-12-theming`

**Shipped**
- Backend `internal/theme`: 8 presets (`presets.json`, go:embed), WCAG validator (7 pairs), `Resolve` precedence (custom → preset → legacy primary → ledger), `GET /themes/presets`, `theme` block on `GET/PUT /org/branding` + public branding/unit endpoints, `org_themes` upsert, audit `branding.theme_update`, 400 problem+json with `failures[]`. Drift guard test against the UI copy.
- `packages/ui` theme v2: `ThemeTokens`/`ResolvedTheme`, `deriveTokens` (15 CSS vars, mixes toward paper so dark presets work), `validateTheme` (same pairs as server), `applyOrgTheme` v2 (+ legacy input), `resetOrgTheme`, generated `PRESETS`; tokens.css tokenised, `--accent` used for links/active nav, dark stamp step.
- Tenant Branding: preset gallery with live mini-ledger previews, Advanced panel (7 colours + font, live contrast badges, Save disabled on failure, server failures surfaced), live page preview with revert, no-flash cache `tms.tenant.theme`; design-system "Themes" section.
- Enduser: `lib/theme.ts` adapter (v2 + legacy), full theme applied pre-auth (QR landing/connect) and signed-in, cache migrated to v2 shape, `theme-color` meta from paper, dark-aware document/signature/letterhead, print pinned to light paper.

**Verified**: `make build/test/lint` green; curl: 8 presets, preset save → public endpoints, low-contrast 400 with failures, legacy PUT; browser with JJnE on Night ledger: tenant reports dark (paper #14161c, stamp-paid #4ec07f, charts re-stepped), renter rent book dark with theme-color #14161c. JJnE restored to `ledger`.
