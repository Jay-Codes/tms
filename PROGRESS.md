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
