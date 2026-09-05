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
