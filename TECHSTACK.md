# TECHSTACK.md

Canonical tech stack for TMS. All architecture and implementation decisions must conform to this document.

## Overview

| Layer | Technology |
|-------|------------|
| Frontend | Next.js (CSR — client-side rendering) |
| Backend | Go (Golang) |
| Cache | Redis |
| Database | PostgreSQL |
| Object storage | MinIO (S3-compatible buckets) |
| Deployment | Docker Compose |

## Frontend — Next.js (CSR)

- Next.js App Router, rendered client-side. No SSR/RSC data fetching for app pages — components use `"use client"` and fetch data from the Go API in the browser.
- The frontend is a pure API consumer. No Next.js API routes (`app/api`) for business logic — all business logic lives in the Go backend.
- Data fetching from the browser to the Go backend over REST (JSON). Recommended: TanStack Query for caching/retries, or plain `fetch`.
- Auth tokens handled client-side (httpOnly cookie set by the Go backend preferred over localStorage).
- TypeScript throughout.

## Backend — Go

- Single Go service exposing a REST JSON API consumed by the Next.js frontend.
- Standard project layout: `cmd/` for entrypoints, `internal/` for application code.
- HTTP: `net/http` with a lightweight router (chi or similar) — avoid heavy frameworks.
- Configuration via environment variables.
- Structured logging (`log/slog`).
- Database access via `pgx` (no heavy ORM; `sqlc` acceptable for generated queries).

## Cache — Redis

- Used for: session storage, API response caching, rate limiting, short-lived state.
- Accessed only from the Go backend (never directly from the frontend).
- Client: `go-redis`.
- Cache is always treated as ephemeral — the app must function (degraded) if Redis is empty or down.

## Database — PostgreSQL

- Single source of truth for all persistent relational data.
- Schema managed with versioned migrations (e.g. `golang-migrate`). No manual schema changes.
- Accessed only from the Go backend.

## Object Storage — MinIO

- All file/blob storage (uploads, documents, images, exports) goes to MinIO buckets via the S3 API.
- Accessed from the Go backend using the MinIO Go SDK (or AWS S3 SDK pointed at MinIO).
- Frontend never talks to MinIO directly except via presigned URLs issued by the Go backend.
- No files written to local disk for persistence.

## Deployment — Docker Compose

- Docker Compose is the deployment mechanism for every environment (dev infra and full-stack deploys).
- Dev: `docker-compose.yml` runs Postgres, Redis, MinIO (+ bucket init) — `make up` / `make down`. App processes run on the host via `make preview`.
- Full stack (`--profile full`): built images for the Go API, the three Next.js apps, and the Go proxy; TLS terminates at the proxy.
- Server (`docker-compose.deploy.yml`, `make docker-deploy`): Go API + edge proxy only, bound to loopback behind the host's TLS reverse proxy; Next.js apps on Vercel; Postgres/Redis/MinIO reused from the host or run under `INFRA=own`.
- Configuration via `.env` (gitignored; `.env.example` committed). Data lives in named volumes.

## Rules

1. Frontend renders client-side and holds no business logic.
2. Go backend is the only component that touches Postgres, Redis, and MinIO.
3. Postgres = durable data. Redis = ephemeral cache. MinIO = blobs. Never mix roles.
4. Any deviation from this stack requires updating this document first.
