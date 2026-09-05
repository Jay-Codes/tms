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
                     cache, rate    letterheads,
                     limits, jobs)  QR PNGs, KYC docs)
```

- Single Go service (`cmd/api`), internal packages per domain (`internal/org`, `internal/renter`, `internal/contract`, `internal/payment`, `internal/notify`, `internal/audit`, `internal/report`).
- Router: chi. DB: pgx + sqlc. Migrations: golang-migrate. Logging: slog.
- Frontends are pure API consumers; no business logic client-side.
- All three apps are **PWAs**: web app manifest + service worker (app-shell caching only; no offline writes in MVP).

### 2.0 Design system

Base design system lives in `packages/ui` (`@tms/ui`), used by all three apps. Previews: `/enduser/design-system` (mobile, renter) and `/tenant/design-system` (desktop, landlord). Concept: **"the stamped ledger"** — a landlord's rent book. Paper background, blue-black ballpoint ink for text, ledger-blue rules, money right-aligned in tabular figures with accountant's double rule under totals, and a rubber **stamp** for what has happened (Paid / Overdue); pending is only pencilled. No card grids or drop shadows except genuinely separate sheets (dialogs). Two layers:

- **Core (fixed platform-wide):** paper/ink/rule palette, stamp colors (never themed), 16px-base type scale with one display size, 4px spacing grid, small stationery radii (3–6px), 52px ledger rows / 44px touch minimum, Solar icon set (Iconify), ink-colored focus ring, reduced-motion respected. Shared classes: `.ledger`, `.stamp`, `.pencil`, `.amount`, `.num`, `.btn-*`, `.field`/`.input`, `.sheet`, `.tabs`/`.tab`, `.bottom-bar`.
- **Org theme (landlord-configurable, enduser + tenant apps only):** a **preset** plus an optional **advanced token override**, and one font from a whitelist (Bricolage Grotesque default, Archivo, Instrument Sans, Hanken Grotesk — self-hosted via next/font). Applied at runtime by `applyOrgTheme()` from org branding settings; the admin app is **never themed** — it always uses platform defaults. Identity lives in structure, so a color/font swap cannot break a screen.

**Theme model (Part 2).** The themable set is exactly eight tokens:

| Token | Role |
|-------|------|
| `paper` | page background |
| `surface` | sheets, cards, dialogs |
| `ink` | body text |
| `ink-muted` | secondary text |
| `rule` | ledger lines and borders |
| `primary` | primary action (pressed / tint / on-primary derived automatically) |
| `accent` | highlights, chips, chart accent |
| `font` | font id from the whitelist |

Everything else is **fixed platform-wide and never themed**: stamp colors (Paid / Overdue), the focus ring, the 4px spacing grid, the stationery radii (3–6px) and the 16px-base type scale. Eight curated **presets** ship with the platform — Ledger (default), Night ledger, Warm paper, Cool slate, Forest, Ocean, High-contrast, Minimal white — each a complete, AA-validated token set. The advanced panel exposes the individual tokens with live contrast badges.

**Contrast guard.** `validateTheme(tokens)` in `packages/ui/theme` returns the failing pairs (`ink`/`paper`, `ink-muted`/`paper`, `on-primary`/`primary`, `ink`/`surface`) with their ratios; the client warns live and the backend **re-validates on save**, rejecting any body-text pair below **WCAG AA 4.5:1** with `400` and the failing pairs listed. The same resolved token set is served to the renter app, so **one theme covers both the landlord and the renter apps**; an org with no theme resolves to preset `ledger`.

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

### 3.2 Language (per user)

Every user carries a locale — `users.locale`, `sw` or `en`, default `sw` — **chosen at registration/signup** and changeable afterwards (renter Profile, landlord Settings → Preferences). The locale drives both the screen language and every SMS sent to that person, including bulk sends. `orgs.settings.sms_language` survives only as the **fallback for renters with no preference** (pre-existing users), and is relabelled accordingly in the UI.

Public pages seen before sign-in (QR landing, connect, org login) default to the **org language** and show a visible **SW/EN toggle**; the visitor's choice persists locally and is carried into registration as the initial `locale`. The admin app stays English-only.

---

## 4. Data model (PostgreSQL)

All tables: `id UUID PK`, `created_at`, `updated_at`. Org-scoped tables include `org_id FK`. Soft delete (`deleted_at`) on user-visible entities.

```
orgs                 name, slug, status, settings JSONB (auto_approve_links, due_day, grace_days, reminder offsets)
payment_periods      org_id, label ("Monthly", "3 weeks"), days INT >0, is_recommended, sort_order, active
                     -- landlord-managed list; seeded with presets 30/90/180/365 days,
                     -- landlord adds any custom value (7, 21, 45 days...) — no upper/lower cap beyond >0
                     -- partial unique index: at most ONE recommended period per org
                     --   UNIQUE (org_id) WHERE is_recommended AND deleted_at IS NULL
org_branding         org_id, display_name, logo_object_key, letterhead_object_key NULLABLE,
                     theme JSONB {primary_color, font_id}, dashboard_prefs JSONB,
                     document_footer_text NULLABLE       -- address/phone/signature line under contracts
                     -- font_id from whitelist (bricolage|archivo|instrument|hanken); see packages/ui
org_themes           org_id PK, preset_id NULLABLE, tokens JSONB NULLABLE, font_id
                     -- preset_id from the 8 shipped presets; tokens = advanced per-token override
                     -- (paper, surface, ink, ink_muted, rule, primary, accent); NULL both = preset "ledger"
users                phone, email, password_hash, kind (renter|org_user|platform_admin), status,
                     locale TEXT NOT NULL DEFAULT 'sw' CHECK (locale IN ('sw','en'))
org_members          org_id, user_id, role (org_owner|org_manager)
renter_profiles      user_id, full_name, nida_number, next_of_kin_name, next_of_kin_phone, kyc_status, kyc_doc_object_key
properties           org_id, name, location_text, lat/lng NULLABLE, notes
units                org_id, property_id, name, unit_code UNIQUE, status (vacant|occupied|unlisted|maintenance),
                     allowed_period_ids UUID[] NULLABLE   -- NULL = all org periods offered for this unit
price_plans          org_id, unit_id, amount, currency (TZS), period_days (default 30), effective_from
                     -- price history preserved; amount is per `period_days`, other periods prorated
contract_templates   org_id, name, body_html, is_default   -- edited in-app (rich text editor); sanitized HTML
contracts            org_id, unit_id, renter_user_id, template_id, terms_snapshot_html,
                     -- app-native document: rendered in-app from snapshot + org letterhead; no external file
                     rent_amount, rent_period_days,           -- snapshot of price basis
                     payment_period_id, payment_period_days,  -- chosen cadence (days snapshotted)
                     term_days, start_date, end_date,         -- span; end_date = start + term_days
                     due_day NULLABLE, status (draft|pending_signature|active|expiring|ended|terminated),
                     snapshot_hash                            -- sha256 over terms_snapshot_html + key fields
contract_signatures  org_id, contract_id, party (renter|landlord), user_id, method (otp_accept|drawn),
                     otp_ref NULLABLE, signature_object_key NULLABLE, snapshot_hash, ip, user_agent, signed_at
                     -- append-only; one row per party; renter row required before activate
unit_link_requests   org_id, unit_id, renter_user_id, status (pending|approved|rejected)
payment_schedules    org_id, contract_id, period_start, period_end, due_date, amount,
                     status (pending|paid|partial|overdue|waived)
payments             org_id, contract_id, schedule_id NULLABLE, amount, method
                     (cash|bank_transfer|mobile_money_manual), reference, paid_at,
                     recorded_by_user_id, note, status (recorded|confirmed|reversed)
expense_categories   org_id, name, is_default, sort_order, active
                     -- seeded: Repairs & maintenance, Utilities, Security, Cleaning,
                     --         Taxes & levies, Insurance, Management fees, Other
expenses             org_id, property_id, unit_id NULLABLE, category_id, amount (TZS), incurred_on (date),
                     vendor, reference, note, receipt_object_key NULLABLE, recorded_by_user_id,
                     status (recorded|voided), voided_at, void_reason, deleted_at
notification_log     org_id, user_id, kind (reminder_7d|reminder_due|overdue_daily|thank_you|otp|custom),
                     channel (sms), dedupe_key UNIQUE, payload, provider_msg_id, status, sent_at
                     -- status: queued|sending|sent|failed|held_no_credit
org_sms_credits      org_id PK, balance INT NOT NULL DEFAULT 0, low_watermark INT NOT NULL DEFAULT 50,
                     updated_at
sms_credit_ledger    org_id, delta INT, balance_after INT, reason (topup|adjust|debit|refund),
                     notification_id NULLABLE, admin_user_id NULLABLE, note, created_at
                     -- append-only (trigger); no expiry, no monthly reset
platform_templates   kind PK, sw TEXT, en TEXT, variables TEXT[], locked BOOL NOT NULL DEFAULT false,
                     updated_by_admin_id, updated_at, version INT      -- `otp` seeded locked
platform_template_versions  kind, version, sw, en, admin_user_id, created_at   -- history, append-only
audit_log            org_id NULLABLE, actor_user_id, action, entity_type, entity_id,
                     before JSONB, after JSONB, ip, user_agent, at   -- append-only, no UPDATE/DELETE grants
sessions             token_hash, user_id, org_id NULLABLE, expires_at
```

Key rules:

- **Contracts snapshot terms and rent** at signing — later template/price edits never mutate active contracts.
- **Payment periods are landlord-defined, in days, unlimited.** Each org has a `payment_periods` list seeded with four presets (30 / 90 / 180 / 365 days, restorable). Landlord adds/edits/deactivates any custom period (7, 21, 45 days…); only constraint is `days > 0`. Units may restrict to a subset via `allowed_period_ids`.
- **Exactly one recommended period per org.** `is_recommended` is a single badge, not a class of presets: bootstrap sets it on **Monthly (30 days)**, the landlord moves it in Settings → Periods (`POST /org/payment-periods/{id}/recommend`, audited), and a partial unique index enforces the "at most one" rule. The recommended period sorts first wherever periods are offered (renter unit page, connect, contract creation), which is what makes that ordering meaningful.
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
GET  /public/units/{unit_code}    branding + resolved theme tokens + unit summary + offered payment
                                  periods with prorated amounts, recommended first (pre-auth, rate-limited)
GET  /public/orgs/{slug}/branding branding + resolved theme tokens (preset merged with overrides)
GET  /themes/presets              the 8 shipped presets with their token sets (public, cacheable)
```

### 5.2 Org & branding (landlord)
```
POST /orgs                        register org (owner signup)
GET/PATCH /org                    current org profile & settings
GET/PUT   /org/branding           display name, theme, dashboard prefs; `theme` is
                                  {preset_id|null, tokens:{…}|null, font_id} — server re-validates
                                  contrast and rejects < 4.5:1 body text (400 + failing pairs)
POST      /org/branding/logo      → presigned MinIO upload URL
POST      /org/branding/letterhead → presigned upload (PNG/JPG banner shown atop contract documents)
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
                                  seeded on org creation with 30/90/180/365, Monthly recommended
POST /org/payment-periods/{id}/recommend   moves the single "Recommended" badge to this period (audited)
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
CRUD /contract-templates          terms management (rich-text body, sanitized HTML; variables
                                  {{renter_name}}, {{unit}}, {{property}}, {{rent}}, {{rent_basis}},
                                  {{start_date}}, {{end_date}}, {{payment_period}}, {{org_name}},
                                  {{term_days}}, {{due_day}})
POST /contracts                   from link approval or manual: unit + renter + template +
                                  term_days + payment_period_id + start_date (end_date derived) + due_day?
POST /contracts/{id}/sign/otp     renter: send OTP to registered phone for signing (rate-limited)
POST /contracts/{id}/sign         renter: {otp_code, signature_upload_key?} → verifies OTP, records
                                  contract_signatures row (party=renter), status → pending landlord
POST /contracts/{id}/signature-upload   presigned PUT for optional drawn signature PNG (bucket `signatures`)
POST /contracts/{id}/activate     landlord: records landlord signature row (party=landlord, method
                                  otp_accept via session), requires renter signature present; snapshots
                                  price, generates schedules, status → active
POST /contracts/{id}/terminate
GET  /contracts, /contracts/{id}  (renter sees only own)
GET  /contracts/{id}/document     app-native document: {letterhead_url, logo_url, org display name,
                                  terms_snapshot_html, parties, schedule summary, footer_text,
                                  signatures[] {party, name, signed_at, method, phone_masked,
                                  signature_image_url?}, snapshot_hash}. Rendered in-app with a
                                  signature block; "Print / Save as PDF" is browser print with a
                                  print stylesheet. No server-side PDF in MVP.
GET  /contracts/{id}/verify       recompute hash; returns valid/tampered + signature summary
```

**Rent in the document (Part 2 rule).** `{{rent}}` is the amount **per payment period**, not the unit's price basis:

```
{{rent}}       = round(rent_amount × payment_period_days / rent_period_days)   -- schedule proration rounding
{{rent_basis}} = the unit price with its own basis, e.g. "TZS 100,000 / 30 days"
```

So a 100,000-per-30-days unit on a Quarterly (90-day) cadence renders "TZS 300,000 per Quarterly (90 days) (TZS 100,000 / 30 days)". The default template sentence is "rent of **{{rent}}** per {{payment_period}} ({{rent_basis}})", and the document header's Rent row shows the per-payment-period figure with the basis underneath. `GET /contracts/{id}` and the renter's read carry `rent_per_period` alongside `rent_amount` / `rent_period_days` / `payment_period_days`. The **snapshot rule is unchanged**: existing contracts keep the terms HTML they were signed with, so only contracts created after this change render the new figure, and `snapshot_hash` is unaffected (the hash covers the rendered terms).

**Digital signing (MVP):**
- Terms are snapshotted (variables resolved) and `snapshot_hash` computed when the contract enters `pending_signature`. Nothing about the document can change after that — edits require a new contract.
- **Renter signs** in-app: reads the document → taps "Accept & sign" → OTP to the phone they registered with → optional drawn signature on a canvas → signature row stored with OTP reference, IP, user agent, timestamp, hash.
- **Landlord countersigns** by activating (authenticated org user; row recorded the same way). Both rows are append-only and audited.
- Document renders a signature block: "Signed by {name} on {date} via phone •••{last4}" (+ drawn image if given), and the hash for verification. Either party can re-open and print anytime.
- Evidence bundle (snapshot + hash + OTP proof + IP/UA + timestamps) is the MVP e-signature; no third-party e-sign provider. Certificate-based / provider-backed signing is a §9 seam.

Contract documents are **app-native**: the source of truth is `terms_snapshot_html` in Postgres, rendered by the frontends with the org's letterhead/logo. No generated files are stored. A server-rendered PDF (§9) can be added later without schema change.

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
GET  /org/notification-settings   offsets, enable/disable kinds, sender name, default language for
                                  renters with no preference; templates read-only for locked kinds
PUT  /org/notification-settings   an override of a locked kind → 409 template_locked
POST /notifications/custom        landlord bulk/targeted SMS to renters; body given as {body_sw?, body_en?}
                                  (at least one) and fanned out per recipient locale, falling back to the
                                  other language when only one is given; the language used is logged.
                                  Pre-checks credits → 409 insufficient_sms_credits {needed, balance}
GET  /notifications/log
GET  /org/sms-credits             {balance, low_watermark, held_count}
```

### 5.9 Reports & audit
```
GET /reports/summary              total assets (property & unit counts), total renters,
                                  occupancy rate, collected vs expected in the resolved window
GET /reports/payment-status       per renter: paid | pending | overdue (+ CSV export)
GET /reports/collections          collections over time
GET /reports/revenue              expected vs collected vs expenses vs net, bucketed series + trend
GET /reports/occupancy            units occupied per bucket end
GET /audit-log?entity=&actor=&from=&to=      (org-scoped; admin sees all)
```

**Cadence (Part 2).** `/reports/summary`, `/reports/payment-status`, `/reports/collections`, `/reports/revenue`, `/reports/occupancy` and `/expenses/summary` all accept the same window parameters: `cadence=month|quarter|half_year|year|custom` with `from` / `to` (required for `custom`) and an optional `anchor` date. Windows resolve on the Dar es Salaam wall clock (`internal/period`), and every response **echoes the resolved `{from, to, cadence}` plus the equivalent `previous` window**, so period-over-period comparisons are computed from one place. Series endpoints add `bucket=day|week|month` (auto: `day` for ≤ 62 days, `week` for ≤ 26 weeks, `month` otherwise), zero-filled, capped at 400 buckets, and accept `property_id` / `group_by=property`.

### 5.10 Platform admin
```
GET /admin/orgs                   list/suspend/activate orgs
GET /admin/metrics                platform health
```

### 5.11 Expenses (landlord)
```
CRUD /org/expense-categories      category list (name, sort_order, active); seeded with 8 defaults
POST /expenses                    {property_id, unit_id?, category_id, amount, incurred_on, vendor?,
                                  reference?, note?} — amount bounds, incurred_on ≤ today+1,
                                  property in org, unit belongs to the property
PATCH /expenses/{id}              audited before/after
POST /expenses/{id}/void          {reason} — append-style correction, like a payment reversal;
                                  the row stays, status → voided, nothing is restored
GET  /expenses?property_id=&unit_id=&category_id=&from=&to=&cursor=   (+ format=csv, formula-neutralised)
POST /expenses/{id}/receipt       presigned upload into bucket `receipts` + complete/view
GET  /expenses/summary?cadence=&from=&to=&group_by=property|category  → totals per group + grand total
```

### 5.12 Platform admin: templates & SMS credits
```
GET   /admin/templates                     all kinds, both languages, variables, version, last editor
PUT   /admin/templates/{kind}              {sw, en} — placeholders validated against the kind's
                                           allowed variables; records a version; audited
PATCH /admin/templates/{kind}              {locked} — a locked kind refuses org overrides
POST  /admin/templates/{kind}/preview      {language, sample?} → rendered body
POST  /admin/templates/{kind}/revert       {version} → restores that version as a new version
GET   /admin/orgs/{id}/sms                 {balance, low_watermark, used_30d, held_count, ledger:[…]}
POST  /admin/orgs/{id}/sms/topup           {credits, note}
POST  /admin/orgs/{id}/sms/adjust          {delta, note}
PATCH /admin/orgs/{id}/sms                 {low_watermark}
```
All four credit routes are audited twice: the platform audit trail and the org's own log (visible to the landlord as "credits added by platform").

### 5.13 Locale
```
PATCH /me                         renter: {locale:"sw"|"en"} (audited)
PATCH /org/members/me             org user: {locale:"sw"|"en"} (audited)
POST  /auth/register/renter       accepts locale (from the public SW/EN toggle)
POST  /orgs                       owner signup accepts locale
GET   /auth/me, GET /me           return `locale` in the session payload
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

Mechanics: scheduler derives due sends from Postgres → `dedupe_key` (`{kind}:{schedule_id}:{date}`) prevents duplicates → Redis queue → worker calls Beem with retry/backoff (3 attempts) → result stored in `notification_log`. Templates support variables (`{{name}}, {{amount}}, {{due_date}}, {{property}}, {{unit}}, {{org}}, {{next_due_date}}, {{link}}`); org-overridable.

**Language resolution (Part 2).** `notify.LanguageFor(recipientUser, org)` = the **recipient's `users.locale`**, falling back to `orgs.settings.sms_language` when the user has no preference. Every queue writer passes the recipient's locale, not the org's, and the scheduler groups by recipient locale. A bulk send fans out per recipient (`body_sw` / `body_en`).

**Template resolution.** Org override → `platform_templates` row → built-in Go default. The platform defaults are seeded into `platform_templates` by migration, so the table is authoritative from day one; the Go constants remain only as the last-resort fallback. A **locked** kind (per-kind `locked` flag, `otp` locked out of the box) shows the landlord read-only wording and refuses an override on `PUT /org/notification-settings` with **409 `template_locked`**. The rendered-template cache in Redis is invalidated on save.

**SMS credits.** Each org holds a prepaid balance (`org_sms_credits`, topped up by the platform admin — **no expiry, no monthly reset**). Every outbound SMS **except the exempt kinds** (`otp` and other security messages, configurable list) debits **1 credit per 160-character GSM segment** (70 for UCS-2) at **send time**, as one conditional update (`SET balance = balance - n WHERE balance >= n`) with an append-only `sms_credit_ledger` row carrying `balance_after`. Insufficient balance leaves the row **`held_no_credit`** — not `failed`; a top-up releases held rows **in queue order**. A bulk send pre-checks the balance and refuses with **409 `insufficient_sms_credits {needed, balance}`** so the landlord is told before anything is queued. The landlord sees the balance, the held count and a low-watermark warning in Notifications.

---

## 7. Object storage (MinIO)

Buckets: `branding` (logos, letterheads), `qrcodes` (unit QR PNGs; S3 requires ≥3-char names), `kyc` (ID document images — private, short-TTL presigned reads only), `signatures` (drawn signature PNGs — private, presigned reads only for contract parties), `receipts` (expense receipts, key `{org_id}/{expense_id}.{ext}`, ≤ 5 MiB, `image/jpeg` / `image/png` / `application/pdf` — private, presigned reads only for the org). No `contracts` bucket in MVP — contract documents are app-native (§5.5). All access via backend-issued presigned URLs; uploads via presigned PUT with content-type and size limits enforced on completion callback.

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
- **Server-rendered contract PDFs:** would add a `contracts` bucket + `GET /contracts/{id}/pdf`; `terms_snapshot_html` + `contract_signatures` already hold everything needed to render.
- **Provider-backed / certificate e-signature:** `contract_signatures.method` gains a new value; existing evidence rows stay valid.

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

Part 2 (post-MVP iteration, plan in [PLAN2.md](PLAN2.md)):

| Phase | Content |
|-------|---------|
| 9 | Foundations + end-to-end fixes: single recommended period, rent per payment period in the document, layout-level nav shell, mobile contract fix, `PeriodPicker`, `internal/period`, Part 2 migrations + `receipts` bucket |
| 10 | Expenses: categories, ledger with receipts, void semantics, CSV, per-property and all-properties summary |
| 11 | Reports v2: cadence everywhere, revenue/expenses/net time series with trends, occupancy series, per-property breakdown |
| 12 | Theming v2: 8 presets + advanced token override with the contrast guard, applied to both landlord and renter apps |
| 13 | Language: `users.locale` per user, SMS + bulk SMS in the recipient's language, UI i18n (SW/EN) |
| 14 | Platform admin: prepaid SMS credits per org and DB-backed platform templates with per-kind locking |
| 15 | Mobile landlord pass, performance and isolation hardening, seed v2, UAT 2 |
