# TMS — Tenancy Management System

White-label multi-tenant SaaS for landlords: properties, units, renters, contracts, rent collection, SMS reminders. First client brand: **JJnE Rentals**.

**Status:** pre-alpha scaffold. Spec locked, design system in place, frontends are shells, backend not started. Target: testing-ready **15 Sep 2026**.

## Stack

| Layer | Tech |
|-------|------|
| Frontend | Next.js 15 (CSR, TypeScript) × 3 apps, PWA |
| Backend | Go (chi, pgx, sqlc, golang-migrate) — planned |
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
  ui/          @tms/ui design system: tokens.css, theme.ts (applyOrgTheme), ThemeSwitcher
proxy/         Go reverse proxy :8080 → apps by path prefix (+ /api later)
docker-compose.yml   postgres, redis, minio + bucket init (branding, qr, kyc, signatures)
Makefile       single entry point for all commands
```

Docs: [SPEC.md](SPEC.md) (architecture, data model, API) · [FLOWS.md](FLOWS.md) (user flows) · [PLAN.md](PLAN.md) (phased implementation plan) · [DEV.md](DEV.md) (dev loop).

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
| `make build` / `make test` / `make lint` | Build, test, lint backend + frontends |
| `make images` | Build all deployable Docker images |
| `make deploy` | `images` + bring the full compose profile up |
| `make seed` | Seed a load-test org (`load@tms.local`/`password123`: 5 properties, 50 units, 40 renters, contracts, payments) |
| `make seed-demo` | Demo data for JJnE Rentals (overdue/paid/pending contracts, link request; owner `demo@jjne.test`/`password123`) |
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

## Deploy (full-stack compose)

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
| `BEEM_*`, `NOTIFY_WORKERS` | Beem SMS credentials and the size of the sending pool |

## What exists today

- Three Next.js apps wired to `@tms/ui` (org-themable fonts via `next/font`: Bricolage Grotesque default, Archivo, Instrument Sans, Hanken Grotesk). Admin app is fixed to platform theme.
- `@tms/ui` v0.2 — **"stamped ledger"** design system (landlord's rent book: paper background, ink text, ledger rules, right-aligned tabular amounts, rubber-stamp Paid/Overdue, pencilled Pending). Core tokens: paper/ink/rule palette, stamp colors, 16px type scale, 4px grid, 52px ledger rows / 44px touch minimum, Solar icons. Runtime org theming (`applyOrgTheme()`: one primary color + one whitelisted font), theme switcher. Shared classes: `.ledger`, `.stamp`, `.pencil`, `.amount`, `.btn-*`, `.field`/`.input`, `.sheet`, `.tabs`, `.bottom-bar`.
- Go dev proxy, Make workflow, ngrok preview.
- Compose infra definition (validated, not yet exercised).
- Full spec, flows, and 8-phase plan.

## What's next

Phase 0–1 of [PLAN.md](PLAN.md): compose infra up, `backend/` skeleton, migrations for full schema, auth (OTP + password), org onboarding, audit middleware.

## Key design decisions

- Single DB, shared schema, mandatory `org_id` on every org-scoped query.
- QR-first renter onboarding: each unit has a permanent QR → `/enduser/u/{unit_code}`.
- Payment periods are landlord-defined in **days** (recommended presets 30/90/180/365 + unlimited custom, e.g. 7/21/45). Contract span and payment cadence are separate; schedules generated up front with proration.
- Contracts snapshot terms + price at activation. Contract documents are **app-native**: rich-text template edited in-app, rendered with the org's uploaded letterhead/logo, printed via browser — no server-side PDF in MVP. Digitally signed in-app: renter accepts via OTP to registered phone (+ optional drawn signature), landlord countersigns on activation; snapshot hash + OTP/IP/UA evidence stored append-only.
- MVP payments are offline-recorded; gateway (scan-to-pay) is a reserved post-MVP seam.
- Append-only audit log on every mutation.

## UAT

Testing checklist: [docs/UAT.md](docs/UAT.md) (flows 1–11, accounts, OTP-from-log). Load results: [docs/LOADTEST.md](docs/LOADTEST.md).
