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

## External providers

| Provider | Used for | Reached how |
|----------|----------|-------------|
| Beem Africa | Outbound SMS (`notify.BeemProvider`) | REST from the Go backend only; `BEEM_API_KEY`, `BEEM_SECRET_KEY`, `BEEM_SENDER_ID` |
| Snippe (snippe.sh) | Mobile-money collection: landlords buy SMS credits (Phase 27) | REST from the Go backend only (see below) |

### Snippe — payment provider (Phase 27)

- **What**: a Tanzanian mobile-money gateway. `POST /v1/payments` sends a USSD push to the payer's phone (`payment_type: "mobile"`); the payer approves on the handset. TZS only, integer amounts, minimum 500 TZS; Snippe keeps a 2.5% fee.
- **Outbound**: the Go backend calls `SNIPPE_BASE_URL` (default `https://api.snippe.sh`) with `Authorization: Bearer $SNIPPE_API_KEY` and an `Idempotency-Key` (the order's own ≤30-char code). `GET /v1/payments/{reference}` reconciles orders the webhook has not settled. The client lives in one file (`backend/internal/snippe`) and is tested against a fake Snippe (httptest); nothing else talks to Snippe.
- **Inbound**: Snippe calls `POST /api/v1/webhooks/snippe` — a public route on the Go API, reached through the same edge proxy / nginx `/api` prefix as every other call (no proxy change). Every delivery is verified (`X-Webhook-Signature` = hex HMAC-SHA256 of `{timestamp}.{raw body}` keyed by `SNIPPE_WEBHOOK_SECRET`, constant-time compare, timestamp within 5 minutes) and deduplicated by event id in Postgres before it can credit anything.
- **Secrets**: `SNIPPE_API_KEY`, `SNIPPE_WEBHOOK_SECRET` in `.env` / `.env.deploy` only (never in the frontends, never logged). `SNIPPE_WEBHOOK_URL` is the public URL Snippe is told to call (derived from `PUBLIC_BASE_URL`/`APP_BASE_URL` + `/api/v1/webhooks/snippe` when unset — correct for the single-origin proxy layout; set it explicitly when the API has its own host).
- **Off by default**: with the key or the signing secret unset, the purchase routes answer 503 `purchases_disabled`; everything else works.
- The frontends never talk to Snippe; they call the Go API and poll the order.

## Rules

1. Frontend renders client-side and holds no business logic.
2. Go backend is the only component that touches Postgres, Redis, and MinIO.
3. Postgres = durable data. Redis = ephemeral cache. MinIO = blobs. Never mix roles.
4. Any deviation from this stack requires updating this document first.
