# Kickoff — Phases 18.2 (enduser items), 19, 20

Paste everything below the line into a fresh Claude Code session (model: Fable 5.1) started in `/Users/jay/Documents/DevWork/Growth/TMS`. It runs four Opus 5 lanes in parallel, one per domain, each owning its own directory and its own branch.

---

You are the **orchestrator** for TMS Phases 19 and 20 plus the four Phase 18.2 enduser items scoped on 20 Sep 2026. You run on Fable 5.1. You do not write the bulk of the code; you brief four **Opus 5 lanes** (`Agent` tool, `model: "opus"`, `run_in_background: true`), integrate, verify, commit, and report. I am not watching; use the audio cues in `ORCHESTRATOR_PROMPT.md` §5. Everything in `ORCHESTRATOR_PROMPT.md` §2 (environment), §5 (audio) and §6 (definition of done) still applies. Read it first.

## 1. Read first

`CLAUDE.md`, `TECHSTACK.md`, `TOOLING.md`, `PLAN2.md` (Phase 18.2 lines 237–246, Phase 19, Phase 20 — this is the checklist), `API.md` (live contract), `DECISIONS.md`, `PROGRESS.md` (last three entries), `SPEC.md` §3 / §5.10 / §8, `FLOWS.md` flows 2, 3, 4, 14. Decisions in PLAN2 are locked; if a line is silent, pick the simplest compliant option and add a `DECISIONS.md` row.

## 2. State you inherit

- Branch `phase-18-assisted-onboarding` holds Phase 18 backend (committed, `bd2c38a`) and the scope commit `50dfd1d`. Phase 18.2 tenant/enduser assist UI may be **in progress in another session** on that same branch — `git status` / `git log` before anything, and **do not touch** `apps/tenant/app/(portal)/renters/assist*`, the assist bits of `apps/enduser/app/u/[unit_code]/*`, register/login/sign pages' assist hint, or `backend/internal/httpserver/assist_handlers.go`.
- Latest migration is `000020_assist_sessions`. Backend lane owns **000021** onward.
- Dev accounts, ports, restart targets: `PROGRESS.md` header and the memory notes — landlord `owner@jjne.test` / `password123` (org `jjne-rentals`), renter `0755000111` PIN `1234`, admin `admin@tms.local` / `admin12345`. Backend `make api-restart`, proxy `make proxy-restart`, apps `make apps-restart`. **Never** `make preview/dev/stop`. Postgres host port 5433.
- Concurrent lanes deadlock on `tms_test`: lanes run their **own package** tests only (`go test ./internal/httpserver/ -run 'TestPhase19|TestPhase20'` etc.); you run the full `go test -p 1 ./...` once lanes are quiet.

## 3. Branching

One branch per lane off `phase-18-assisted-onboarding`:

| Lane | Branch | Owns (write) | Read-only elsewhere |
|------|--------|--------------|---------------------|
| backend | `phase-19-20-backend` | `backend/**`, `API.md` (new sections only), `DECISIONS.md` (append) | everything else |
| admin | `phase-19-admin` | `apps/admin/**` | `packages/ui` (propose, don't edit), `API.md` |
| tenant | `phase-19-20-tenant` | `apps/tenant/**` | same |
| enduser | `phase-18-20-enduser` | `apps/enduser/**` | same |

Lanes commit on their own branch with small imperative commits (this is the one change from the old template: lanes **do** commit, so a lane killed by the Opus session limit loses nothing). You merge each lane branch `--no-ff` into `phase-18-assisted-onboarding` after its exit criteria pass, then that branch into `main` when all four are in. Shared files (`packages/ui`, `Makefile`, `proxy/`) are yours alone — a lane that needs a change there reports it, you apply it.

## 4. Order of work

1. **Backend lane starts first** and, within its first hour, commits `API.md` sections "Phase 19" and "Phase 20" with the request/response shapes below (it may refine them; frontends read the file, not this prompt). Ping the three frontend lanes with the commit hash.
2. **Admin, tenant, enduser lanes start at the same time as backend**, building against the shapes below, and reconcile to `API.md` once the backend commit lands. Frontend-only items (18.2 all four, 20.1 file picker, 20.2 logo) do not wait for anything.
3. When a lane reports done: review its diff, run its tests, exercise the flow in the browser at `http://localhost:8080/{admin|tenant|enduser}`, then merge.
4. After all four merge: full test suite, `make build`, `make lint`, a **review-only** Opus lane (SPEC §8: org isolation on every new endpoint, audit row on every mutation, NIDA never in a URL/log/list response, presigned URLs never cached beyond expiry), a fix lane if needed, then tick PLAN2 boxes, `PROGRESS.md` entry, `docs/UAT.md` rows, merge to `main`, Glass cue.

## 5. Endpoint contract (starting point — backend lane owns the final shape in API.md)

```
# 19.1 / 19.2 NIDA reveal
POST /renters/{user_id}/nida/reveal     org (owner, manager)   {reason?: string ≤200}
POST /admin/users/{id}/nida/reveal      admin                  {reason: string ≤200}   (reason required)
  → 200 {nida_number, full_name, revealed_at}
  → 404 no relationship with this org (org audience) / unknown user
  → 429 org limiter 60/h
  audit renter.nida_reveal {entity: user, after: {reason, actor_kind}}
GET /me/profile → adds nida_reveals: [{at, by_kind: "landlord"|"platform_admin", org_name?}] (last 10)

# 19.2 admin user directory
GET  /admin/users?q=&kind=renter|org_user|platform_admin&status=active|suspended&org_id=&cursor=&limit=
  → {items: [{id, kind, full_name, phone, email, status, created_at,
              orgs: [{id, name, role?, relationship?: "renting"|"applied"|"past"}],
              contracts_live?: n, kyc_status?}], next_cursor}
  (no nida field of any kind on the list)
GET  /admin/users/{id}
  → {user: {…list row…, nida_masked?}, link_requests: [...last 10], contracts: [...],
     payments: {count, total, last_paid_at}, memberships: [...], audit: {items, next_cursor}}
  audit admin.user_view
POST /admin/users/{id}/suspend {reason}   POST /admin/users/{id}/activate {reason}
  → 200 {user}; suspend revokes sessions; audit admin.user_suspend / admin.user_activate

# 19.3 names
PATCH /org/members/me            {full_name}                 org user, self       audit member.update
PATCH /org/members/{id}          {full_name?, role?}         owner only; 409 last_owner
PATCH /renters/{user_id}         {full_name}                 owner, manager; 409 renter_signed once any signed contract exists
PATCH /admin/users/{id}          {full_name, reason}         admin; audit admin.user_update
  all: 422 on length (2–80 after trim); before/after in audit; renter SMS name_corrected, org user e-mail

# 20.1 proof amount lock
POST /me/proofs  — when schedule_id given, amount must equal row outstanding → 422 amount_mismatch {expected}

# 20.3 backfill
POST /contracts            start_date accepted down to 10 years back for landlord-created contracts
                           (renter application keeps the 7-day backstop; import renters same as manual)
POST /contracts/{id}/backfill   org (owner, manager)
  {until: "YYYY-MM-DD", mode: "paid"|"waived", paid_at?: "YYYY-MM-DD"|"due_date", method?, reference?, note?}
  → 200 {settled, skipped, total}
  → 409 contract_not_active; 422 until in the future / before start_date
  paid: one payment per unpaid row (source=backfill, paid_at default = row due_date), audit payment.record
  waived: rows → waived with note
  one SMS backfill_done (kind can be disabled), never per-row
GET /contracts/{id}/schedules, GET /me/schedules → each row gains last_payment_source: "manual"|"import"|"backfill"|null
GET /payments?source=manual|import|backfill  (+ CSV column)
```

## 6. Lane briefs

Launch all four in **one message**. Each brief below is complete; paste it as the `prompt`.

### 6.1 Backend lane

```
Role: backend worker on TMS, Phases 19 + 20 (+ 20.1 amount lock). Model: Opus 5.
Repo: /Users/jay/Documents/DevWork/Growth/TMS. Branch: create `phase-19-20-backend` off `phase-18-assisted-onboarding`. Write only under backend/**, plus new sections in API.md and appended rows in DECISIONS.md. Commit small and often on your branch.
Read first: CLAUDE.md, TECHSTACK.md, TOOLING.md, PLAN2.md Phase 19 (all of 19.0–19.3) and Phase 20 (20.1 backend bullet, 20.3 all), API.md (existing Phase 16/18 sections for house style), SPEC.md §5.10 §8, backend/internal/httpserver/{renter_directory_handlers,link_handlers,contract_handlers,payment_handlers,proof_handlers,admin_org_handlers,org_handlers,import_handlers}.go, backend/internal/audit/audit.go, backend/internal/payment/*.go (allocator).
Do not touch: assist_handlers.go, migration 000020, anything under apps/.
Task, in this order (commit after each):
 1. API.md: add "Phase 19" and "Phase 20" sections with the final request/response shapes (start from KICKOFF_19_20.md §5, refine as you implement). Commit within the first hour — three frontend lanes are waiting on it.
 2. Migration 000021: users.status already exists; add renter_profiles.nida_reveals? NO — reveals live in audit_log. Add payments.source TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual','import','backfill')) and backfill existing import rows (source='import' where the import chip is derived today — find how "imported" is detected and migrate that to the column). Add a schedules-side nothing; last_payment_source is a query join.
 3. 19.1 NIDA reveal: POST /renters/{user_id}/nida/reveal (owner+manager, relationship check = any link request or contract for this renter in this org, else 404), per-org limiter 60/h, audit renter.nida_reveal; GET /me/profile gains nida_reveals (last 10 from audit_log). Never log the number; never put it in a GET.
 4. 19.3 names: PATCH /org/members/me, PATCH /org/members/{id} (owner; last_owner 409), PATCH /renters/{user_id} (409 renter_signed if any contract_signatures row by this renter exists anywhere), PATCH /admin/users/{id}. Each writes users.full_name (+ renter_profiles.full_name for renters), audits before/after, queues the notification (SMS name_corrected for renters via the existing template system — add the kind + SW/EN default templates; e-mail for org users via the existing mailer). Contract snapshot untouched — add a test that the document + verify hash are byte-identical after a rename.
 5. 19.2 admin directory: GET /admin/users (search: phone normalised, email lower, name ILIKE prefix; filters; cursor paging; no NIDA), GET /admin/users/{id} (aggregate; audit admin.user_view), POST /admin/users/{id}/nida/reveal (reason required), POST suspend/activate (revoke sessions like org suspend). Add the new kinds to the admin audit filter list.
 6. 20.1: POST /me/proofs with schedule_id → amount must equal outstanding → 422 amount_mismatch {expected}.
 7. 20.3: POST /contracts accepts start_date down to 10 years back (manual + import renters; the renter application in link_handlers keeps startBackstopDays=7); POST /contracts/{id}/backfill per PLAN2 (paid/waived, skips paid/partial, one SMS backfill_done — add the kind + templates, disable-able in notification settings); last_payment_source on both schedule lists; ?source= filter on GET /payments + CSV column; import payments preview error text points at Backfill when a row predates the first schedule. Check report queries bucket by paid_at not created_at; add a test with a backfilled year.
Constraints: every mutation = org scope + audit row + validation; run the org-isolation census (isolation_suite_test.go) with the new routes added; run only `go test ./internal/httpserver/ -run 'Phase19|Phase20|Isolation'` and `go vet ./...` (the orchestrator runs the full suite). No make preview/dev/stop. Backend restart: make api-restart.
Report (≤40 lines): endpoints shipped with any deviation from KICKOFF §5, migration number, test summary, DECISIONS rows added, anything blocked.
```

### 6.2 Admin lane

```
Role: frontend-admin worker on TMS, Phase 19.2 + 19.3 admin parts. Model: Opus 5.
Repo: /Users/jay/Documents/DevWork/Growth/TMS. Branch: create `phase-19-admin` off `phase-18-assisted-onboarding`. Write only under apps/admin/**. Commit small and often on your branch. Do not edit packages/ui — if you need a shared component change, describe it in your report.
Read first: CLAUDE.md, TECHSTACK.md, PLAN2.md Phase 19.2 and the admin bullets of 19.1/19.3, API.md (Phase 19 section once the backend lane commits it — until then use KICKOFF_19_20.md §5), apps/admin/app/(portal)/orgs/[id]/page.tsx and orgs/page.tsx (list + detail house style), apps/admin/app/(portal)/audit/page.tsx (paging pattern), apps/admin/lib/api.ts, apps/admin/i18n/{en,sw}.ts.
Task:
 1. lib/api.ts: adminUsers.list/get/suspend/activate/rename/revealNida with the §5 shapes (typed).
 2. `(portal)/users/page.tsx`: search box (phone / e-mail / name), kind + status filters, table (name, kind, phone, e-mail, status, orgs, created), "Show older" cursor paging. Nav entry "Users". Empty + error states in house style.
 3. `(portal)/users/[id]/page.tsx`: header (name, kind chip, status, Suspend/Activate with reason dialog), tabs Overview (identity, orgs/memberships, kyc_status, nida_masked with a "Reveal NIDA" button → confirm sheet with required reason → shows the full number for 60 s with Copy, then re-masks; never store it), Tenancies (link requests + contracts), Payments (summary + link to org), Activity (audit rows, paged). "Edit name" inline with reason (PATCH /admin/users/{id}).
 4. Org detail page: Members and Renters rows link to the user detail.
 5. SW/EN strings for everything.
Constraints: CSR only, no business logic client-side; reveal is a POST and the value never lands in URL, localStorage or logs; run `make lint` scoped to apps/admin if a target exists, else `npx tsc --noEmit -p apps/admin`. No make preview/dev/stop; `make apps-restart` only if the dev server wedges. Verify in the browser at http://localhost:8080/admin with admin@tms.local / admin12345.
Report (≤30 lines): pages + routes shipped, any API shape you had to assume or found different in API.md, shared-component requests, tsc/lint result, screenshots taken.
```

### 6.3 Tenant lane

```
Role: frontend-tenant worker on TMS, Phases 19.1, 19.3 (landlord parts), 20.2, 20.3 (UI). Model: Opus 5.
Repo: /Users/jay/Documents/DevWork/Growth/TMS. Branch: create `phase-19-20-tenant` off `phase-18-assisted-onboarding`. Write only under apps/tenant/**. Do NOT touch apps/tenant/app/(portal)/renters/assist* or any file whose header mentions Phase 18 assist (another session may be working there). Commit small and often on your branch. Do not edit packages/ui — describe needed changes in your report.
Read first: CLAUDE.md, TECHSTACK.md, PLAN2.md 19.1, 19.3, 20.2, 20.3, API.md (Phase 19/20 sections when the backend lane commits them; until then KICKOFF_19_20.md §5), apps/tenant/components/Shell.tsx (OrgMark), apps/tenant/lib/branding.ts, apps/tenant/app/(portal)/renters/[user_id]/page.tsx, link-requests/[id]/page.tsx, contracts/[id]/page.tsx, settings/page.tsx (staff list), components/RecordPaymentSheet.tsx and Sheet.tsx (sheet house style), components/PaymentBits.tsx (chips), lib/api.ts, i18n/{en,sw}.ts.
Task:
 A. 20.2 logo (no backend dependency — do first): keep logo_url from /org/branding in the cached theme with its expiry; OrgMark renders the logo 28×28 on a white tile, falls back to the primary square; onError → refetch once → fallback; branding page reapplies after upload/delete.
 B. 19.1 NIDA reveal: renter detail + link-request detail: masked value stays; "Show full number" → confirm sheet (one-line why, optional reason) → POST reveal → full number 60 s with Copy → re-mask. Never persisted client-side.
 C. 19.3 names: Settings → Staff: owner sees "Edit" per row (name, role; last-owner error surfaced); own name editable in Settings (PATCH /org/members/me). Renter detail "Edit name" (PATCH /renters/{id}), disabled with the renter_signed hint once the API says so.
 D. 20.3 backfill UI: contract page "Backfill history" sheet (visible while any unpaid row has due_date < today): until-date picker (default last full period before today), Paid (method, one paid-at for all or "use each due date", reference, note) or Waived (reason), preview "N periods · TZS X" from the schedules already loaded, confirm → POST backfill → refresh. Same sheet reachable from the Overdue list row menu ("This is old history? Backfill…"). "backfilled" pencil chip next to "imported" wherever payment chips render; Payments list gains a Source filter. New contract form + renters import: date picker allows the past with the hint "this tenancy started before TMS — past periods will be generated". Import payments preview: show the backend's Backfill hint text.
 E. SW/EN strings for everything.
Constraints: CSR only; no business logic client-side (preview totals are display math on loaded rows, the server decides); `npx tsc --noEmit -p apps/tenant`; verify at http://localhost:8080/tenant with owner@jjne.test / password123. No make preview/dev/stop.
Report (≤30 lines): screens shipped, API shapes assumed vs found, shared-component requests, tsc result, screenshots.
```

### 6.4 Enduser lane

```
Role: frontend-enduser worker on TMS, Phase 18.2 enduser items (4) + 19 renter-side + 20.1. Model: Opus 5.
Repo: /Users/jay/Documents/DevWork/Growth/TMS. Branch: create `phase-18-20-enduser` off `phase-18-assisted-onboarding`. Write only under apps/enduser/**. Do NOT touch the assist parts of app/u/[unit_code]/*, register, login or contract/[id]/sign pages (another session may be working there); if 18.2 work needs those files, edit only the lines you must and say so in the report. Commit small and often on your branch. Do not edit packages/ui.
Read first: CLAUDE.md, TECHSTACK.md, PLAN2.md lines 237–246 (18.2 — the four Enduser items: unit code input, home hero per unit, history paging, past vs current), 19.1 enduser bullet, 20.1 enduser bullets, 20.3 enduser bullet, API.md (Phase 19/20 when committed; until then KICKOFF_19_20.md §5), apps/enduser/app/page.tsx, payments/page.tsx, contract/page.tsx, profile/page.tsx, components/ProofSheet.tsx, lib/api.ts, lib/scan.ts, i18n/{en,sw}.ts, apps/enduser/app/u/[unit_code]/page.tsx (read only, to know the 404 handling).
Task (no backend dependency for 1–5, do them first):
 1. Unit code input on home (`home.hint.none` state): "Have a unit code?" field, 10 chars Crockford, uppercase/trim, submit → router.push(`/u/${code}`).
 2. Home hero per unit: when >1 live contract, one due card per contract (earliest unpaid schedule from /me/schedules items, unit · property, countdown chip, its own Send proof); single contract keeps today's layout.
 3. History paging: "Show older" on payments History, Proofs you sent, and the contracts list, driven by next_cursor; types updated in lib/api.ts.
 4. Past vs current: contracts page sections Current / Past (collapsed with count); payments page ended-contract groups under a collapsed "Past tenancies"; overdue total + hero count live contracts only.
 5. 20.1 ProofSheet: two actions — "Take photo" (accept="image/*" capture="environment") and "Choose a file" (jpeg/png/pdf, no capture); amount becomes a static Money line when opened from a schedule row (target.scheduleId set), editable only from the generic button; on 422 amount_mismatch re-read the row and show the server's expected amount.
 6. 19.1: Profile → "Who has seen your NIDA" list from /me/profile nida_reveals.
 7. 20.3: "backfilled" chip on schedule rows/history (last_payment_source), same style as "imported".
 8. SW/EN strings for everything.
Constraints: CSR only; phone-width first (16px gutters, no horizontal scroll); `npx tsc --noEmit -p apps/enduser`; verify at http://localhost:8080/enduser with 0755000111 / PIN 1234. No make preview/dev/stop.
Report (≤30 lines): items shipped, which assist-adjacent files you touched (exact lines) if any, API shapes assumed vs found, tsc result, screenshots (mobile viewport).
```

## 7. Exit criteria (all must hold before merging to main)

- PLAN2 boxes for 18.2 (four enduser items), 19.0–19.3, 20.1–20.3 ticked with evidence in `PROGRESS.md`.
- `go test -p 1 ./...`, `make build`, `make lint` green.
- Walked in the browser: landlord reveals a NIDA and the renter sees it under "Who has seen"; admin finds a user by phone, suspends and reactivates; owner renames a manager; landlord renames an unsigned renter and gets 409 on a signed one; renter sends a PDF proof from the file picker with a locked amount; logo shows in the rail and mobile bar; a contract started two years back is backfilled and reports bucket the money by paid_at.
- Review lane findings fixed. `docs/UAT.md` rows added. Glass cue.
