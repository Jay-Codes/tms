# PLAN.md — TMS Implementation Plan

Execution plan for [SPEC.md](SPEC.md) / [FLOWS.md](FLOWS.md). Target: **testing-ready 15 September 2026** (10 days from 5 Sep). Stack per [TECHSTACK.md](TECHSTACK.md), tooling per [TOOLING.md](TOOLING.md).

## Current state (done)

- Monorepo: npm workspaces — `apps/enduser`, `apps/tenant`, `apps/admin` (Next.js CSR hello pages with basePaths), `packages/ui` (`@tms/ui` design system: tokens, org theming via `applyOrgTheme()`, font whitelist).
- `proxy/` Go reverse proxy (:8080 → :3001/:3002/:3003), `make preview` dev loop with ngrok.

## Guiding rules

- Backend-first: every screen consumes a real endpoint; no mocked business logic in frontends.
- Every mutating endpoint ships with audit-log write + org-scope check from day one — not retrofitted.
- Schema lands early and completely (all §4 tables in the first migration set); features fill in behind it.
- Daily deploy to the compose stack; ngrok preview stays the client-visible demo.

---

## Phase 0 — Foundations & infrastructure (Sep 5, 0.5 day)

- [x] `docker-compose.yml`: postgres:17, redis:7, minio + bucket-init job (`branding`, `qrcodes`, `kyc`, `signatures`). Volumes, healthchecks. `make up` / `make down`.
- [x] Backend skeleton `backend/`: `cmd/api`, `internal/{org,renter,contract,payment,notify,audit,report,platform}`, chi router, slog, config from env, `/healthz`.
- [x] golang-migrate wiring + `make migrate`; sqlc config; `make build`, `make test`, `make lint` (golangci-lint, next lint).
- [x] Proxy routes `/api/*` → backend :8081 (frontends keep 3001-3003).

**Exit:** `make up && make migrate && make preview` gives working `/api/v1/healthz` through ngrok.

## Phase 1 — Schema, auth, org onboarding (Sep 5–6, 1.5 days)

- [x] Migrations: all §4 tables (orgs, org_branding, users, org_members, renter_profiles, properties, units, price_plans, payment_periods, contract_templates, contracts, unit_link_requests, payment_schedules, payments, notification_log, audit_log, sessions). Seed recommended payment periods (30/90/180/365d) on org creation. pgcrypto for `nida_number`. Audit table: no UPDATE/DELETE grants.
- [x] Repository layer: org_id mandatory param guard (§2.1).
- [x] Sessions: opaque token, httpOnly cookie, Redis + Postgres fallback. argon2id.
- [x] Renter auth: register → Beem OTP → PIN; login phone+PIN; OTP rate limits (Redis). Beem client stubbed behind interface (real creds later; dev mode logs OTP).
- [x] Landlord/admin auth: email+password, verification link. RBAC middleware (`org_owner`, `org_manager`, `renter`, `platform_admin`).
- [x] `POST /orgs` (owner signup), `GET/PATCH /org`, org member CRUD (§5.2).
- [x] Audit middleware: every mutation → audit_log row.
- [x] Frontend: tenant-app signup/login + wizard shell (Flow 1); enduser login/register/OTP screens.

**Exit:** org can be created, owner logs in, staff invited; renter registers with OTP (dev-logged); cross-org access returns 404 (first isolation tests green).

## Phase 2 — Properties, units, QR, pricing, vacancy (Sep 7, 1 day)

- [x] CRUD properties + units; `unit_code` (unguessable base32).
- [x] QR generation → PNG → MinIO `qr` bucket, presigned download; per-unit + bulk-per-property print sheet (§3.1, §5.3).
- [x] Price plans: `{amount, period_days, effective_from}`, history endpoint; bulk update.
- [x] Payment periods: `CRUD /org/payment-periods` (unlimited custom day-counts, recommended flag, ordering, deactivate); per-unit `allowed_period_ids`; settings screen + wizard step.
- [x] Vacancy board `GET /units?status=vacant`; status overrides.
- [x] `GET /public/units/{unit_code}` + `GET /public/orgs/{slug}/branding` (rate-limited).
- [x] Tenant app: properties/units screens, price history, vacancy board, QR print (Flows 4, 5).

**Exit:** landlord creates property + units, prints QRs; scan URL resolves branding + unit summary pre-auth.

## Phase 3 — Renter onboarding, KYC, linking (Sep 8, 1 day)

- [x] `/enduser/u/{unit_code}` landing: org-branded (applyOrgTheme from public branding), unit summary, register/login CTA (Flow 2).
- [x] KYC: `GET/PUT /me/profile` (NIDA encrypted, masked last-4 in UI), optional ID upload → presigned PUT to `kyc`. *(backend)*
- [x] Link requests: create from scan, landlord inbox, approve/reject, auto-approve org setting; occupied-unit edge case. *(backend)*
- [x] Renter directory + KYC view — endpoints live; tenant-app screens outstanding.
- [x] SMS notifications on approve/reject (queued + minimal worker; full scheduler in Phase 6). *(backend)*
- [x] Schedule generator `internal/contract.Generate` (pure, table-tested) — Phase 4 reuses it for `payment_schedules`.

**Exit:** full Flow 2 end-to-end with auto-approve; manual approval via landlord inbox.

## Phase 4 — Contracts & schedules (Sep 9, 1 day)

- [x] Contract template CRUD (rich-text HTML, sanitized allowlist, default flag, preview).
- [x] Contracts: create (from approval or manual), activate = snapshot terms + price basis + period days, generate all payment_schedules across `term_days` at `payment_period_days` cadence, last row truncated, amounts prorated from price basis, optional `due_day` snapping (§4 rules). Table-driven tests: 180d/45d, 100d/30d, 7d cadence, due_day snapping.
- [x] Renter onboarding + public unit endpoint show offered periods with prorated amounts and schedule preview.
- [x] Lifecycle: draft → pending_signature → active → expiring (30d job) → ended | terminated. Terminate cancels/waives remaining schedules, unit → vacant.
- [x] `GET /contracts/{id}/document`: app-native document payload (letterhead/logo URLs, resolved `terms_snapshot_html`, parties, schedule summary, footer). Frontend document view + print stylesheet (browser "Save as PDF"). No server PDF.
- [x] Rich-text template editor in tenant app (sanitized HTML, variable insertion, live letterhead preview); letterhead upload + footer text in branding settings.
- [x] Digital signing: `snapshot_hash` at pending_signature; `contract_signatures` table; `POST /contracts/{id}/sign/otp` + `/sign` (OTP verify, IP/UA capture); optional drawn signature (canvas → presigned PUT to `signatures` bucket); activate requires renter signature and records landlord row; `GET /contracts/{id}/verify`.
- [x] Screens: templates editor (with snapshot-rule banner), contract detail (both apps), renter "Accept & sign" flow (document → OTP → optional draw → confirmation), landlord "Ready to countersign" → Activate, signature block + hash in document view.
- [x] Unsigned-contract reminder (org setting, default 7 days) wired into Phase 6 scheduler.

**Exit:** approval → renter OTP-signs → landlord activates → correct schedule rows; document renders with letterhead + signature block, prints cleanly, `/verify` returns valid; termination flips unit vacant.

## Phase 5 — Payments (offline) & statuses (Sep 10, 1 day)

- [x] `POST /payments` record (method, reference, note); full → `paid`, under → `partial`, over → roll to next schedule with confirm prompt (Flow 7). *(backend; allocation is the pure, table-tested `internal/payment.Allocate`)*
- [x] `POST /payments/{id}/reverse` (audited, schedule reverts, overdue recomputed). *(backend)*
- [x] Overdue job: hourly ticker + `POST /admin/jobs/overdue` + on-demand org-scoped flip on every schedule read. *(backend)*
- [x] `GET /schedules`, `GET /payments`, `GET /payments/{id}`, `GET/PUT /org/bank-account`, renter `GET /me/schedules` (next due, overdue total, bank account) and `GET /me/payments`. *(backend)*
- [x] Renter dashboard: next due, status chip, history, org bank-account instructions. Landlord: overdue views, record-payment form. *(frontend outstanding)*

**Exit:** Flow 7 complete; statuses correct across paid/partial/overdue/reversed.

## Phase 6 — Notifications end-to-end (Sep 11, 1 day)

- [x] Scheduler goroutine (5-min tick) derives due sends from Postgres; dedupe_key; Redis queue; worker pool (N=3, atomic claim) → Beem, 3-attempt backoff (§2.2, §6).
- [x] Kinds: reminder_7d, reminder_due, overdue_daily, unsigned_reminder, thank_you (+next due), otp, custom — all through one `notify.Render`. Templates with variables, SW/EN per org.
- [x] Org notification settings endpoints; custom bulk SMS (rate-limited, audited); notification log + retry endpoints. *(backend; tenant screens are frontend work)*
- [ ] Live Beem credentials smoke-tested. **Deferred: no Beem account/creds available.** The transport is implemented and covered by an `httptest` server; dev runs still use the LogProvider (DECISIONS.md).

**Exit:** timeline of Flow 8 fires against a test contract with real SMS.

## Phase 7 — Reports, branding, admin app, PWA (Sep 12, 1 day)

- [x] Reports: summary (assets, renters, occupancy, collected vs expected), payment-status per renter + CSV, collections over time (§5.9). Dashboard prefs validated on `PUT /org/branding` (Flow 9). *(backend; dashboard screens are frontend work)*
- [ ] Branding: `GET/PUT /org/branding`, logo presigned upload, theme applied in enduser + tenant apps; admin stays platform-default. *(endpoints live since Phase 4; theme wiring in the apps outstanding)*
- [x] Admin endpoints: `GET /admin/orgs`, `GET /admin/orgs/{id}`, suspend/activate (403 `org_suspended` enforced in `RequireOrg`), `GET /admin/metrics`, cross-org `GET /admin/audit-log`, `GET /admin/jobs` (Flow 11). *(backend; admin app screens outstanding)*
- [ ] Audit page (org-scoped filterable) in tenant app.
- [ ] PWA: manifest + service worker (app-shell cache only) on all three apps.

**Exit:** dashboards live; JJnE Rentals branding demo-able; admin can suspend an org.

## Phase 8 — Hardening & UAT prep (Sep 13–14, 2 days)

- [ ] Isolation test suite: cross-org 404 on every org-scoped route (§8).
- [ ] Rate-limit pass (auth, OTP, public QR). Input-validation sweep.
- [ ] Load pass: seed script (1 org, 5 properties, 50 units, 40 renters); p95 checks on hot endpoints.
- [ ] Full-stack compose profile: build images for backend + 3 apps + proxy; one-command bring-up.
- [ ] UAT script from FLOWS.md; seed demo data for JJnE Rentals; bug triage buffer.

**Exit (Sep 15):** demo environment up via compose + ngrok; UAT checklist executed clean.

---

## Deployment — Docker Compose

Compose is the deployment stack (dev infra now, full stack at Phase 8):

- `docker-compose.yml` (infra, always on in dev): postgres, redis, minio, minio bucket-init. Volumes for data; healthchecks; env via `.env` (gitignored, `.env.example` committed).
- `full` profile (Phase 8): + `api` (Go multi-stage image), `enduser`/`tenant`/`admin` (Next standalone builds), `proxy`. Prod TLS terminates at the proxy.
- Make targets: `make up`, `make down`, `make migrate`; `make deploy` (Phase 8) = build images + `docker compose --profile full up -d`.

## Risks

| Risk | Mitigation |
|------|------------|
| 10-day window, 1 phase/day | Scope is MVP-locked; anything ambiguous defaults to simplest spec-compliant option; §9 seams stay untouched |
| Beem account/sender-ID approval delays | Provider behind interface; dev mode logs SMS; only Phase 6 needs live creds |
| Payment edge cases (partial/overpay/reverse) | Table-driven unit tests written with Phase 5, not after |
| Org isolation regressions | Isolation tests run in `make test` from Phase 1 onward |
