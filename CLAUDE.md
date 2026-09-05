# CLAUDE.md

## Tech stack

The authoritative tech stack for this project is defined in [TECHSTACK.md](TECHSTACK.md). Read it before making any architectural or implementation decisions, and conform to it:

- **Frontend:** Next.js, client-side rendered (CSR). No business logic in the frontend; it consumes the Go REST API.
- **Backend:** Go — the only layer that talks to Postgres, Redis, and MinIO.
- **Cache:** Redis (ephemeral only).
- **Database:** PostgreSQL (source of truth, versioned migrations).
- **Object storage:** MinIO buckets via S3 API (frontend accesses files only through presigned URLs from the backend).
- **Deployment:** Docker Compose (`make up`/`make down` for infra; full-stack profile for deploys).

If a change requires deviating from the stack, update TECHSTACK.md first, then implement.

## Spec & plan

Product spec: [SPEC.md](SPEC.md) (architecture, data model, API surface) with user flows in [FLOWS.md](FLOWS.md). Implementation plan and phase status: [PLAN.md](PLAN.md) — consult it before starting work; update checkboxes as phases complete.

## Tooling

Developer tooling is defined in [TOOLING.md](TOOLING.md): **Make** is the single task runner (all repeatable commands live in the root `Makefile`), and **Git** is the only version control tool. Read it before creating any scripts, automation, or workflow commands, and conform to it.

## Dev preview

See [DEV.md](DEV.md): three Next.js apps (`apps/enduser` :3001, `apps/tenant` :3002, `apps/admin` :3003) behind Go reverse proxy `proxy/` on :8080, exposed via ngrok. Public paths: `/enduser`, `/tenant`, `/admin`. Run everything with `make preview`; stop with `make stop`.
