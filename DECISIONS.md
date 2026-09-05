# DECISIONS.md — orchestration decisions where the spec is silent

| Date | Decision | Reason |
|------|----------|--------|
| 2026-09-05 | Renamed default branch `master` → `main`; baseline scaffold (design system, docs, compose) committed as one commit before Phase 0 branch. | TOOLING.md workflow assumes `main`; repo had a single `Init` commit on `master` plus uncommitted work. |
| 2026-09-05 | SMS provider selection by env: `BEEM_API_KEY` empty → `LogProvider` (logs recipient + body incl. OTP to API log); non-empty → Beem. | Beem creds absent; PLAN.md Phase 1 says dev mode logs OTP. |
| 2026-09-05 | Backend runs on host :8081 in dev (`make api`), behind proxy `/api/*`. Migrations run via `go run ./cmd/api migrate up` (golang-migrate as library) so no extra CLI install. | Orchestrator prompt §2; fewer host dependencies. |
| 2026-09-05 | MinIO bucket for unit QR PNGs is `qrcodes`, not `qr` (SPEC §7). | MinIO/S3 rejects bucket names under 3 chars. Compose init uses `--ignore-existing`. |
| 2026-09-05 | Compose Postgres published on host port **5433** (`POSTGRES_HOST_PORT`), `DATABASE_URL` uses :5433. | Host-native Postgres already owns :5432; not ours to stop. |
| 2026-09-05 | One session cookie per audience (`tms_r` renter, `tms_o` org user, `tms_a` admin). | Lets one browser hold renter + landlord sessions during testing; routes are already audience-partitioned. |
| 2026-09-05 | Email delivery behind `EmailProvider` interface; dev implementation logs verification/invite links to the API log. Login is allowed before email verification (`email_verified` flag surfaced in UI banner). | No mail provider in scope; keeps dev loop unblocked. |
| 2026-09-05 | `users.full_name` column added (spec lists it only on renter_profiles). | Org users/admins need a display name for audit + signatures. |
| 2026-09-05 | `make build` for Next apps uses `NEXT_DIST_DIR=.next-build` (`distDir` in next.config) so builds don't clobber the running dev servers' `.next`. | Preview must keep running during orchestration. |
| 2026-09-05 | Client IP: trust `X-Forwarded-For`/`X-Real-IP` only when the direct peer is loopback or in `TRUSTED_PROXY_CIDRS` (default `127.0.0.0/8,::1/128`). | Rate limits + audit IPs must not be spoofable; the Go proxy is the only trusted hop. |
| 2026-09-05 | In `ENV=prod` the API refuses to start with the dev `LogProvider` for SMS/email (missing Beem creds). | OTP codes must never reach production logs. |
| 2026-09-05 | Presigned MinIO URLs are signed against `MINIO_PUBLIC_URL` (default = `APP_BASE_URL`), and the Go proxy forwards `/branding/*`, `/qrcodes/*`, `/kyc/*`, `/signatures/*` to MinIO :9000 without rewriting Host or path. | Presigned `localhost:9000` links are unreachable from phones via ngrok; bucket-name prefixes never collide with app/API paths. |
