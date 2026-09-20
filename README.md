# TMS — Tenancy Management System

White-label multi-tenant SaaS for landlords: properties, units, renters, contracts, rent collection, SMS reminders. First client brand: **JJnE Rentals**.

**Status:** Part 1 (Phases 0–8) and Part 2 Phases 9–14 shipped; Phase 15 (mobile landlord pass, hardening, UAT 2) in progress. Target: testing-ready **15 Sep 2026**. Phase log: [PROGRESS.md](PROGRESS.md).

## Stack

| Layer | Tech |
|-------|------|
| Frontend | Next.js 15 (CSR, TypeScript) × 3 apps, PWA |
| Backend | Go (chi, pgx, sqlc, golang-migrate) |
| Database | PostgreSQL 17 |
| Cache / queue | Redis 7 |
| Object storage | MinIO (S3 API) |
| SMS | Beem Africa |
| Deployment | Docker Compose |
| Task runner | Make |

Details: [TECHSTACK.md](TECHSTACK.md), [TOOLING.md](TOOLING.md).

## Repo layout

```
apps/
  enduser/     Renter app        — basePath /enduser, dev :3001
  tenant/      Landlord portal   — basePath /tenant,  dev :3002
  admin/       Platform admin    — basePath /admin,   dev :3003
packages/
  ui/          @tms/ui design system: tokens.css, theme.ts (applyOrgTheme + presets),
               charts/, i18n/ runtime, PeriodPicker, ThemeSwitcher
backend/       Go API (:8081): cmd/api, cmd/seed, cmd/loadtest, migrations, internal/*
proxy/         Go reverse proxy :8080 → apps by path prefix (+ /api, + MinIO buckets)
docker-compose.yml   postgres, redis, minio + bucket init
               (branding, qrcodes, kyc, signatures, receipts)
Makefile       single entry point for all commands
```

Docs: [SPEC.md](SPEC.md) (architecture, data model, API) · [API.md](API.md) (live endpoint contract) · [FLOWS.md](FLOWS.md) (user flows) · [PLAN.md](PLAN.md) / [PLAN2.md](PLAN2.md) (phase plans) · [PROGRESS.md](PROGRESS.md) · [DECISIONS.md](DECISIONS.md) · [DEV.md](DEV.md) (dev loop).

## Terminology

| Term | Meaning | App |
|------|---------|-----|
| Org | Landlord business — unit of multi-tenancy (`org_id` everywhere) | — |
| Renter | Person renting a unit (client says "tenant") | `apps/enduser` |
| Landlord user | Org staff (owner, manager) | `apps/tenant` |
| Platform admin | TMS operator | `apps/admin` |

## Quick start

Prereqs: Node 24, Go 1.26, Docker, ngrok (for remote preview).

```bash
make install
```

```bash
make up
```

```bash
make preview
```

`make preview` starts the three Next.js dev servers + Go proxy in the background, opens an ngrok tunnel, and prints the public URL. Routes: `/enduser`, `/tenant`, `/admin`; design-system preview at `/enduser/design-system` and `/tenant/design-system`.

Other targets (`make help` lists all):

| Target | Does |
|--------|------|
| `make dev` | Apps + proxy locally, no tunnel (http://localhost:8080) |
| `make url` | Reprint current ngrok URL |
| `make stop` | Kill dev servers, proxy, ngrok |
| `make down` | Stop compose infra (volumes kept) |
| `make api` / `make api-restart` | Build + run the Go API on :8081 (`make api-log` to tail) |
| `make migrate` / `make migrate-down` | Apply / roll back one migration |
| `make test-db` | Create the `tms_test` database used by handler tests |
| `make build` / `make test` / `make lint` | Build, test, lint backend + frontends. **Never run `next build` by hand** — `make build` sets `NEXT_DIST_DIR=.next-build` so it cannot clobber the running dev servers' `.next` |
| `make apps-restart` | Restart only the three Next.js dev servers (proxy, ngrok and the API keep running) |
| `make i18n-check` | Key + placeholder parity between `sw.ts` and `en.ts` in `apps/enduser/i18n` and `apps/tenant/i18n` (also run by `make lint`) |
| `make images` | Build all deployable Docker images |
| `make deploy` | `images` + bring the full compose profile up |
| `make seed` | Seed the load-test org (`load@tms.local`/`password123`): 5 properties, 50 units, 40 renters split SW/EN, contracts, payments spread over a year, 12 months of expenses per property, an opening balance of 500 SMS credits |
| `make seed-demo` | Demo data for JJnE Rentals (overdue/paid/pending contracts, link request, expenses, a custom theme, an edited platform template; owner `demo@jjne.test`/`password123`) |
| `make loadtest` | 20-worker load pass against hot endpoints; results in `docs/LOADTEST.md` |
| `make test-isolation` | Cross-org / cross-renter / admin isolation census (every registered route must be covered) |
| `make test-race` | Race-detector run for httpserver, notify, payment (~2 min) |
| `make deploy-down` | Stop/remove the deployed app containers (infra untouched) |
| `make logs` | Tail the deployed services' logs |
| `make tls-selfsigned` | Write a self-signed cert/key pair into `.dev/tls` |

Logs/pids in `.dev/`. Env config: copy `.env.example` → `.env`.

## Backend

Single Go service, `backend/cmd/api`, serving REST under `/api/v1` on :8081 (chi + pgx + sqlc, `golang-migrate` with the migration files embedded via `go:embed`).

```bash
make api          # build + run in the background
make api-log      # tail .dev/api.log
make migrate      # apply pending migrations
```

- `GET /api/v1/healthz` reports each dependency. Postgres is the only hard dependency — Redis or MinIO being down is degraded-but-serving (200); a down database is 503.
- One binary does three jobs: `api` serves, `api migrate up|down` migrates, and `MIGRATE_ON_START=1` makes the serving process apply migrations before it binds (used by the container image).
- Background workers started with the server: SMS queue drain, notification scheduler, contract lifecycle sweep, overdue-payment sweep.

## Deploy (server: API + edge, frontends on Vercel)

`docker-compose.deploy.yml` runs only the Go API and the edge proxy, bound to `127.0.0.1:3300` for the host's reverse proxy; Postgres/Redis/MinIO are reused from another compose project on the box (default) or run under `INFRA=own`. Config in `.env.deploy` (copy `.env.deploy.example`). Full walkthrough: [docs/DEPLOY.md](docs/DEPLOY.md).

```bash
make docker-builder             # once: x86_64 buildx builder
make docker-publish             # push josephchuchu/tms-{api,edge}:<sha>,latest (linux/amd64)
make docker-deploy              # server: pull + run (reuse existing infra); TAG=… pins
make docker-deploy INFRA=own    # own postgres/redis/minio too
make server-setup               # server: nginx + Let's Encrypt for api.tms.kuzo.co.tz (DOMAIN=…)
make docker-logs                # tail
make docker-down                # remove api + edge
```

## Deploy (full-stack compose, all-in-one)

`docker-compose.yml` carries a `full` profile with built images for the API, the three Next.js apps (standalone output) and the proxy. Infra (Postgres, Redis, MinIO) is the same set of services and the same volumes the dev loop uses.

```bash
make deploy       # build images + docker compose --profile full up -d --wait
make logs         # tail them
make deploy-down  # stop + remove the app containers; infra keeps running
```

Inside the compose network the API reaches `postgres:5432`, `redis:6379` and `minio:9000`, and the proxy fans out to `api:8081`, `enduser:3001`, `tenant:3002`, `admin:3003` and `minio:9000`. Only the proxy publishes ports.

To run a deploy next to a dev preview that already owns :8080, move the published port:

```bash
PROXY_HTTP_PORT=8090 PUBLIC_BASE_URL=http://localhost:8090 make deploy
```

**TLS.** Set `TLS_CERT_FILE` and `TLS_KEY_FILE` (paths inside the proxy container) and the proxy serves HTTPS on 443 and 308-redirects 80 → 443; leave either unset and it serves plain HTTP. `make tls-selfsigned` writes a pair into `.dev/tls`, which is mounted read-only at `/etc/tms/tls`:

```bash
make tls-selfsigned
# then in .env: TLS_CERT_FILE=/etc/tms/tls/cert.pem  TLS_KEY_FILE=/etc/tms/tls/key.pem
```

### Env keys

Full list in `.env.example`. The ones that matter most:

| Key | Meaning |
|-----|---------|
| `ENV` | `dev` or `prod`. `prod` refuses the log-only SMS/email providers and forces `Secure` session cookies |
| `APP_BASE_URL` | Public origin the apps are served from (the ngrok URL in dev) |
| `PUBLIC_BASE_URL` | Origin for public links (QR targets, verification). Defaults to `APP_BASE_URL`; also seeds the deployed API's base URLs |
| `MINIO_PUBLIC_URL` | Origin presigned MinIO URLs are signed against — must be an origin the phone can reach |
| `DATABASE_URL`, `REDIS_URL`, `MINIO_ENDPOINT` | Dev (host) endpoints; the `full` profile overrides all three with in-network names |
| `TRUSTED_PROXY_CIDRS` | Networks allowed to set `X-Forwarded-For`. Dev: loopback. `TRUSTED_PROXY_CIDRS_FULL` is the deployed equivalent (compose bridge space) |
| `PROXY_HTTP_PORT` / `PROXY_HTTPS_PORT` | Host ports the deployed proxy publishes (80/443) |
| `TLS_CERT_FILE` / `TLS_KEY_FILE` | Set both to terminate TLS at the proxy |
| `NIDA_ENC_KEY` | pgcrypto key for encrypted NIDA numbers — rotating it makes existing values unreadable |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` | First platform admin, seeded at startup when none exists |
| `BEEM_API_KEY` / `BEEM_SECRET_KEY` / `BEEM_SENDER_ID` | Beem SMS credentials. Leave `BEEM_API_KEY` empty and the dev `LogProvider` writes every message (OTP codes included) to `.dev/api.log` instead of sending. `BEEM_SENDER_ID` **must be a sender name Beem has approved for the account** — an unapproved one fails every send with `API_INVALID_PARAMETER: Invalid Sender ID` |
| `NOTIFY_WORKERS` | Size of the SMS sending pool (3). Each worker claims its row atomically, so raising it cannot duplicate a message |
| `SMS_CREDIT_EXEMPT_KINDS` | Notification kinds that send without debiting SMS credit. Default `otp`; the literal `none` charges for everything |

## Part 2 at a glance

- **Expenses** — property-level ledger with categories, receipts (bucket `receipts`, JPEG/PNG/PDF ≤ 5 MiB), void-with-reason instead of delete, CSV export, per-property tab and an all-properties summary.
- **Reports v2** — one period picker (month / quarter / 6 months / year / custom) drives every report; revenue vs expected vs expenses vs net as a series with a trend, occupancy over time, per-property breakdown, Δ vs the previous window.
- **Themes** — eight presets (Ledger, Night ledger, Warm paper, Cool slate, Forest, Ocean, High contrast, Minimal white) plus an advanced seven-token override with a live WCAG-AA contrast guard the server re-runs on save. The backend's `presets.json` is the source of truth; the copy in `packages/ui` is generated and pinned by a test. One theme covers the landlord and renter apps; the admin app is never themed; print stays light.
- **Languages** — `users.locale` (`sw`/`en`) per person decides both the screens and every SMS they receive, including bulk sends (two bodies, one per language). Public pages open in the org's language with an SW/EN toggle. Dictionaries live in `apps/*/i18n`; the admin app stays English.
- **SMS credits** — prepaid per org, no expiry, 1 credit per 160-character GSM segment (70 for UCS-2), debited at send time. Out of credit parks a message as `held_no_credit` (never `failed`) until the platform tops the org up. Landlord URL: `/tenant/notifications`.
- **Platform templates** — the admin edits every SMS body in both languages at `{BASE}/admin/templates`, with variable chips, segment counts, preview, version history, revert, and a per-kind lock (`otp` ships locked and is not org-overridable). SMS credit per org: `{BASE}/admin/orgs/{id}` → SMS tab.

## App URLs

Behind the proxy at `{BASE}` (the ngrok URL from `make url`, or `http://localhost:8080`):

| App | Paths |
|-----|-------|
| Renter (`enduser`) | `/enduser`, QR landing `/enduser/u/{unit_code}`, `/enduser/design-system` |
| Landlord (`tenant`) | `/tenant` · properties, units, expenses, reports, payments, contracts, notifications, audit, `/tenant/settings/branding`, `/tenant/design-system` |
| Platform admin | `/admin` · `/admin/orgs`, `/admin/orgs/{id}` (SMS credits tab), **`/admin/templates`**, `/admin/audit`, `/admin/jobs` |

## Key design decisions

- Single DB, shared schema, mandatory `org_id` on every org-scoped query.
- QR-first renter onboarding: each unit has a permanent QR → `/enduser/u/{unit_code}`.
- Payment periods are landlord-defined in **days** (recommended presets 30/90/180/365 + unlimited custom, e.g. 7/21/45). Contract span and payment cadence are separate; schedules generated up front with proration.
- Contracts snapshot terms + price at activation. Contract documents are **app-native**: rich-text template edited in-app, rendered with the org's uploaded letterhead/logo, printed via browser — no server-side PDF in MVP. Digitally signed in-app: renter accepts via OTP to registered phone (+ optional drawn signature), landlord countersigns on activation; snapshot hash + OTP/IP/UA evidence stored append-only.
- MVP payments are offline-recorded; gateway (scan-to-pay) is a reserved post-MVP seam.
- Append-only audit log on every mutation.
- Corrections are **append-style**, never destructive: a payment is reversed, an expense is voided, a template edit writes a version. The only deletion in the ledger is detaching a receipt from an expense.
- Exactly **one** payment period per org carries the "Recommended" badge; the other seeded presets stay restorable but unbadged.
- `{{rent}}` in a contract is the amount **per payment period**; `{{rent_basis}}` keeps the unit's own price. Contracts already signed keep their snapshot verbatim.

## UAT

Testing checklist: [docs/UAT.md](docs/UAT.md) — Part 1 covers flows 1–11 with the accounts and unit codes; **Part 2 (UAT 2)** covers the mobile shell, expenses, reports v2, theming, language, SMS credits and platform templates. Load results: [docs/LOADTEST.md](docs/LOADTEST.md).

**Reading OTPs and SMS in dev.** With `BEEM_API_KEY` empty the log provider is active and every message is written to the API log: `make api-log`, or `tail -f .dev/api.log | grep sms_` — the code is in `sms_body`. With real Beem credentials set, **nothing is logged** and the message must arrive on the handset; a sender ID Beem has not approved fails the send with `API_INVALID_PARAMETER: Invalid Sender ID`. To go back to reading codes from the log, blank `BEEM_API_KEY` in `.env` and `make api-restart`.
