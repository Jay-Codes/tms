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
