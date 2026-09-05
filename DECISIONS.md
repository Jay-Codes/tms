# DECISIONS.md — orchestration decisions where the spec is silent

| Date | Decision | Reason |
|------|----------|--------|
| 2026-09-05 | Renamed default branch `master` → `main`; baseline scaffold (design system, docs, compose) committed as one commit before Phase 0 branch. | TOOLING.md workflow assumes `main`; repo had a single `Init` commit on `master` plus uncommitted work. |
| 2026-09-05 | SMS provider selection by env: `BEEM_API_KEY` empty → `LogProvider` (logs recipient + body incl. OTP to API log); non-empty → Beem. | Beem creds absent; PLAN.md Phase 1 says dev mode logs OTP. |
| 2026-09-05 | Backend runs on host :8081 in dev (`make api`), behind proxy `/api/*`. Migrations run via `go run ./cmd/api migrate up` (golang-migrate as library) so no extra CLI install. | Orchestrator prompt §2; fewer host dependencies. |
| 2026-09-05 | MinIO bucket for unit QR PNGs is `qrcodes`, not `qr` (SPEC §7). | MinIO/S3 rejects bucket names under 3 chars. Compose init uses `--ignore-existing`. |
| 2026-09-05 | Compose Postgres published on host port **5433** (`POSTGRES_HOST_PORT`), `DATABASE_URL` uses :5433. | Host-native Postgres already owns :5432; not ours to stop. |
