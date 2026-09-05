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

Logs/pids in `.dev/`. Env config: copy `.env.example` → `.env`.

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
