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
