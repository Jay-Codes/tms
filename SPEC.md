# SPEC.md — TMS Technical Specification

**Product:** TMS — Tenancy Management System (white-label; first client brand: **JJnE Rentals**)
**Status:** Draft v1 — MVP scope, testing readiness target **15 September 2026**
**Stack:** Per [TECHSTACK.md](TECHSTACK.md) — Next.js CSR PWA frontends, Go REST backend, PostgreSQL, Redis, MinIO. Tooling per [TOOLING.md](TOOLING.md).

---

## 1. Overview

TMS lets landlords manage properties, units, tenants, contracts, and rent collection. It is **multi-tenant SaaS**: each landlord organization is an isolated tenant of the platform. Renters onboard **QR-first** — scanning a QR code affixed to a unit links them to that unit and drops them into registration/login.

### Terminology (important)

| Term | Meaning |
|------|---------|
| **Org** (organization) | A landlord business — the platform's unit of multi-tenancy. All data is scoped by `org_id`. |
| **Renter** | A person renting a unit (the client calls these "tenants"). App: `apps/enduser`. |
| **Landlord user** | Staff member of an org (owner, manager). App: `apps/tenant`. |
| **Platform admin** | TMS operator staff. App: `apps/admin`. |

Code and schema use `org` / `renter` to avoid the tenant/tenant collision.

### MVP scope

**In:** org onboarding, property/unit management, vacancy management, pricing management, contract & terms management, renter KYC onboarding (QR-first), **offline payment recording** (manual entry by landlord, routed to bank collection account outside the system), payment schedules & statuses, SMS notifications via Beem, reports, audit trail, org branding (name, logo, theme, dashboard prefs).

**Out (post-MVP):** payment gateway / scan-to-pay execution, complaints/maintenance requests, lavatory (public toilet) pay-per-use module, in-app chat.

The schema is designed so gateway payments and complaints bolt on without migration pain (see §5.7, §9).

---

## 2. Architecture

```
[enduser PWA]   [tenant PWA]   [admin PWA]      (Next.js CSR, TypeScript)
      \              |              /
       \             |             /
        +---- Go REST API (:8080 behind proxy) ----+
        |   auth · orgs · properties · contracts   |
        |   payments · notifications · reports     |
        +--------------------------------------- --+
           |            |               |        \
       PostgreSQL     Redis          MinIO      Beem SMS
       (truth)      (sessions,     (logos,      (HTTP API)
                     cache, rate    contract
                     limits, jobs)  PDFs, KYC docs)
```

- Single Go service (`cmd/api`), internal packages per domain (`internal/org`, `internal/renter`, `internal/contract`, `internal/payment`, `internal/notify`, `internal/audit`, `internal/report`).
- Router: chi. DB: pgx + sqlc. Migrations: golang-migrate. Logging: slog.
- Frontends are pure API consumers; no business logic client-side.
- All three apps are **PWAs**: web app manifest + service worker (app-shell caching only; no offline writes in MVP).

### 2.0 Design system

Base design system lives in `packages/ui` (`@tms/ui`), used by all three apps; preview at `/enduser/design-system`. Two layers:

- **Core (fixed platform-wide):** warm-gray neutrals, semantic payment-status colors, 1.25 type scale on 16px base, 4px spacing grid, radii, 44px touch targets, Solar icon set (Iconify).
- **Org theme (landlord-configurable, enduser + tenant apps only):** one primary color (strong/soft/on-primary variants derived automatically with contrast guaranteed) and one font from a whitelist (Plus Jakarta Sans default, Inter, Manrope, Figtree — self-hosted via next/font). Applied at runtime by `applyOrgTheme()` from org branding settings; the admin app always uses platform defaults.

### 2.1 Multi-tenancy model

- Single database, shared schema, **`org_id` column on every org-scoped table**.
- Every authenticated request resolves to exactly one `org_id` (from the session); all queries filter by it. A repository-layer guard makes `org_id` a mandatory parameter — no query helper exists without it.
- Platform admin endpoints are separate (`/api/admin/...`) and may cross orgs.
- Org branding (logo URL, theme colors, display name) is served from a public endpoint keyed by org slug so login pages can be branded pre-auth.

### 2.2 Background jobs

Notification scheduling and overdue detection run as a **scheduler goroutine inside the API binary** (MVP simplicity): a ticker scans due schedules every 5 minutes, enqueues SMS jobs into a Redis list, a worker pool consumes and calls Beem. Redis loss is safe — jobs re-derive from Postgres state (`notification_log` dedupes sends).

---

## 3. Identity & auth

- **Renters:** phone-number-first. Register with phone → OTP via Beem SMS → set PIN/password. Login = phone + PIN (OTP fallback).
- **Landlord users & admins:** email + password; email verification link. Optional OTP step later.
- Sessions: opaque token in **httpOnly, Secure, SameSite=Lax cookie**, session data in Redis with Postgres fallback table (`sessions`) so Redis flush doesn't log everyone out.
- Passwords/PINs: argon2id. OTPs: 6-digit, 5-min TTL in Redis, rate-limited (Redis counters: 3 sends / 10 min per phone, 5 verify attempts).
- RBAC roles: `org_owner`, `org_manager` (org scope); `renter`; `platform_admin`. Middleware enforces role + org scope per route.

### 3.1 QR-first onboarding

Each **unit** gets a permanent QR code encoding `https://{host}/enduser/u/{unit_code}` where `unit_code` is an unguessable short ID (e.g. 10-char base32).

Scan flow: resolve `unit_code` → public endpoint returns org branding + unit summary (property name, unit name, price, vacancy status) → renter registers or logs in → system creates a **link request** (`unit_link_requests`) → landlord approves in dashboard (or auto-approve if org setting enabled) → approval creates/activates the contract. QR codes are generated server-side (PNG into MinIO) and printable per-unit or in bulk per-property.

---

## 4. Data model (PostgreSQL)

All tables: `id UUID PK`, `created_at`, `updated_at`. Org-scoped tables include `org_id FK`. Soft delete (`deleted_at`) on user-visible entities.

```
orgs                 name, slug, status, settings JSONB (auto_approve_links, due_day, grace_days, reminder offsets)
payment_periods      org_id, label ("Monthly", "3 weeks"), days INT >0, is_recommended, sort_order, active
                     -- landlord-managed list; seeded with recommended presets 30/90/180/365 days,
                     -- landlord adds any custom value (7, 21, 45 days...) — no upper/lower cap beyond >0
org_branding         org_id, display_name, logo_object_key, theme JSONB {primary_color, font_id}, dashboard_prefs JSONB
                     -- font_id from whitelist (jakarta|inter|manrope|figtree); see packages/ui
users                phone, email, password_hash, kind (renter|org_user|platform_admin), status
org_members          org_id, user_id, role (org_owner|org_manager)
renter_profiles      user_id, full_name, nida_number, next_of_kin_name, next_of_kin_phone, kyc_status, kyc_doc_object_key
properties           org_id, name, location_text, lat/lng NULLABLE, notes
units                org_id, property_id, name, unit_code UNIQUE, status (vacant|occupied|unlisted|maintenance),
                     allowed_period_ids UUID[] NULLABLE   -- NULL = all org periods offered for this unit
price_plans          org_id, unit_id, amount, currency (TZS), period_days (default 30), effective_from
                     -- price history preserved; amount is per `period_days`, other periods prorated
contract_templates   org_id, name, body_md, is_default                                        -- landlord-editable terms
contracts            org_id, unit_id, renter_user_id, template_id, terms_snapshot_md,
                     rent_amount, rent_period_days,           -- snapshot of price basis
                     payment_period_id, payment_period_days,  -- chosen cadence (days snapshotted)
                     term_days, start_date, end_date,         -- span; end_date = start + term_days
                     due_day NULLABLE, status (draft|pending_signature|active|expiring|ended|terminated)
unit_link_requests   org_id, unit_id, renter_user_id, status (pending|approved|rejected)
payment_schedules    org_id, contract_id, period_start, period_end, due_date, amount,
                     status (pending|paid|partial|overdue|waived)
payments             org_id, contract_id, schedule_id NULLABLE, amount, method
                     (cash|bank_transfer|mobile_money_manual), reference, paid_at,
                     recorded_by_user_id, note, status (recorded|confirmed|reversed)
notification_log     org_id, user_id, kind (reminder_7d|reminder_due|overdue_daily|thank_you|otp|custom),
                     channel (sms), dedupe_key UNIQUE, payload, provider_msg_id, status, sent_at
audit_log            org_id NULLABLE, actor_user_id, action, entity_type, entity_id,
                     before JSONB, after JSONB, ip, user_agent, at   -- append-only, no UPDATE/DELETE grants
sessions             token_hash, user_id, org_id NULLABLE, expires_at
```

Key rules:

- **Contracts snapshot terms and rent** at signing — later template/price edits never mutate active contracts.
- **Payment periods are landlord-defined, in days, unlimited.** Each org has a `payment_periods` list seeded with recommended presets (30 / 90 / 180 / 365 days, `is_recommended = true`, shown first in UI). Landlord adds/edits/deactivates any custom period (7, 21, 45 days…); only constraint is `days > 0`. Units may restrict to a subset via `allowed_period_ids`.
- Contract separates **span** (`term_days`) from **cadence** (`payment_period_days`). Creating an `active` contract generates all `payment_schedules` rows up front: one row per cadence interval across the span, last row truncated to `end_date`. Example: 180-day term, 45-day cadence = 4 rows; 100-day term, 30-day cadence = 4 rows (30/30/30/10).
- Schedule amount = `rent_amount × schedule_days / rent_period_days`, rounded to whole TZS (proration from the unit's price basis). Snapshotted at activation like terms.
- `due_day` is optional: if set, due dates snap to that day-of-month (monthly-style periods); if NULL, due date = period start (default for custom day-counts).
- A payment recorded against a schedule flips it `paid` (or `partial` if under amount). Nightly + on-demand job flips past-due `pending` → `overdue`.
- Unit `status` derives from contracts where possible but is stored for explicit landlord overrides (`unlisted`, `maintenance`).

---

## 5. API surface (REST, JSON, `/api/v1`)

Conventions: cursor pagination (`?cursor=&limit=`), RFC-7807-style error bodies, all mutating endpoints write `audit_log`.

### 5.1 Auth & public
```
POST /auth/register/renter        {phone, ...} → OTP sent
POST /auth/otp/verify             {phone, code}
POST /auth/login                  {phone|email, pin|password}
POST /auth/logout
GET  /public/units/{unit_code}    branding + unit summary + offered payment periods with prorated
                                  amounts (pre-auth, rate-limited)
GET  /public/orgs/{slug}/branding
```

### 5.2 Org & branding (landlord)
```
POST /orgs                        register org (owner signup)
GET/PATCH /org                    current org profile & settings
GET/PUT   /org/branding           display name, theme, dashboard prefs
POST      /org/branding/logo      → presigned MinIO upload URL
POST/GET/DELETE /org/members      staff management
```

### 5.3 Properties, units, vacancy, pricing
```
CRUD /properties                  and /properties/{id}/units
POST /units/{id}/qr               (re)generate QR PNG → presigned download
PATCH /units/{id}                 rename, status override (vacancy management)
GET  /units?status=vacant         vacancy board
POST /units/{id}/prices           new price {amount, period_days, effective_from} (pricing management)
GET  /units/{id}/prices           price history
CRUD /org/payment-periods         landlord's period list (label, days, is_recommended, sort_order, active);
                                  seeded on org creation with recommended 30/90/180/365
PATCH /units/{id}                 also sets allowed_period_ids (restrict offered periods per unit)
```

### 5.4 Renters, KYC, linking
```
GET/PUT /me/profile               renter KYC self-service (names, NIDA, next of kin, contacts)
POST /units/{unit_code}/link      renter requests link (from QR scan)
GET  /link-requests               landlord inbox
POST /link-requests/{id}/approve | /reject
GET  /renters                     landlord's renter directory + KYC view
```

### 5.5 Contract templates & contracts
```
CRUD /contract-templates          terms management (markdown body)
POST /contracts                   from link approval or manual: unit + renter + template +
                                  term_days + payment_period_id + start_date (end_date derived) + due_day?
POST /contracts/{id}/activate     snapshots terms & price, generates schedules
POST /contracts/{id}/terminate
GET  /contracts, /contracts/{id}  (renter sees only own)
GET  /contracts/{id}/pdf          rendered terms snapshot → MinIO presigned URL
```

### 5.6 Payments (MVP: offline recording)
```
GET  /me/schedules                renter: upcoming/paid, next due
GET  /schedules?status=overdue    landlord views
POST /payments                    landlord records offline payment {contract_id, schedule_id,
                                  amount, method, reference, paid_at, note}
POST /payments/{id}/reverse       correction (audited)
GET  /payments                    history, filterable; /me/payments for renter
```

### 5.7 Gateway-ready (post-MVP, reserved)
```
POST /payments/intents            create scan-to-pay intent
POST /webhooks/gateway            provider callback → confirm payment
```
`payments.method` gains `gateway`; `payments.status` already supports the lifecycle. No schema break.

### 5.8 Notifications
```
GET  /org/notification-settings   offsets, enable/disable kinds, sender name
PUT  /org/notification-settings
POST /notifications/custom        landlord bulk/targeted SMS to renters
GET  /notifications/log
```

### 5.9 Reports & audit
```
GET /reports/summary              total assets (property & unit counts), total renters,
                                  occupancy rate, collected vs expected this period
GET /reports/payment-status       per renter: paid | pending | overdue (+ CSV export)
GET /reports/collections?from&to  collections over time
GET /audit-log?entity=&actor=&from=&to=      (org-scoped; admin sees all)
```

### 5.10 Platform admin
```
GET /admin/orgs                   list/suspend/activate orgs
GET /admin/metrics                platform health
```

---

## 6. Notifications (Beem SMS)

Provider: **Beem Africa** HTTP API (api key + secret via env vars). Sender ID per org where approved; platform default otherwise.

| Kind | Trigger | Default timing |
|------|---------|----------------|
| `reminder_7d` | schedule due in 7 days | 09:00 EAT |
| `reminder_due` | due date | 09:00 EAT |
| `overdue_daily` | schedule overdue, unresolved | daily 09:00 EAT until paid/waived |
| `thank_you` | payment confirmed | immediate; includes next due date |
| `otp` | auth | immediate |
| `custom` | landlord-initiated | immediate |

Mechanics: scheduler derives due sends from Postgres → `dedupe_key` (`{kind}:{schedule_id}:{date}`) prevents duplicates → Redis queue → worker calls Beem with retry/backoff (3 attempts) → result stored in `notification_log`. Templates support variables (`{{name}}, {{amount}}, {{due_date}}, {{property}}, {{unit}}`); org-overridable, Swahili/English per org setting.

---

## 7. Object storage (MinIO)

Buckets: `branding` (logos), `qr` (unit QR PNGs), `contracts` (rendered PDFs), `kyc` (ID document images — private, short-TTL presigned reads only). All access via backend-issued presigned URLs; uploads via presigned PUT with content-type and size limits enforced on completion callback.

---

## 8. Security & audit

- Every mutating handler emits an append-only `audit_log` row (actor, action, entity, before/after diff, ip, UA). DB role for the app has no UPDATE/DELETE on `audit_log`.
- Org isolation tested explicitly: integration test suite asserts cross-org access returns 404 on every org-scoped route.
- NIDA numbers and KYC docs: encrypted at rest (pgcrypto column encryption for `nida_number`; MinIO SSE for docs), masked in UI (last 4), access audited.
- Rate limiting (Redis): auth endpoints, OTP, public QR resolution.
- Input validation server-side on every endpoint; frontend validation is UX only.
- HTTPS everywhere (ngrok in dev, TLS at proxy in prod).

---

## 9. Deferred-feature seams

- **Complaints:** future `complaints` table (org_id, unit_id, renter_user_id, category, status, thread). No current schema impact.
- **Lavatory pay-per-use + commission split (30/70):** out of scope; would arrive as a separate module with its own service-usage ledger. Explicitly excluded from MVP.
- **Payment gateway:** §5.7.

---

## 10. Delivery plan (high level)

| Phase | Content |
|-------|---------|
| 1 | Backend skeleton, migrations, auth (OTP + password), org onboarding, audit middleware |
| 2 | Properties/units/QR, pricing, vacancy; landlord portal screens |
| 3 | Renter PWA: QR scan → register → link; KYC; link approval |
| 4 | Contract templates, contracts, schedule generation |
| 5 | Offline payments, statuses, overdue job |
| 6 | Beem notifications end-to-end |
| 7 | Reports, branding/theming, dashboard prefs, admin app |
| 8 | Hardening: isolation tests, load pass, UAT — **ready for testing 15 Sep 2026** |
