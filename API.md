# API.md — live endpoint contract (kept in sync per phase; SPEC §5 is the source, this is the concrete shape)

Base: `/api/v1` (same origin as the apps, via proxy). JSON in/out. Errors: RFC-7807 `application/problem+json` `{type,title,status,detail,errors?:{field:msg}}`.
Field validation always answers **400** with `errors` populated (`{"errors":{"phone":"must be a Tanzanian mobile number (07…, 2557…, +2557…)"}}`).
Times: RFC3339 UTC. Money: integer TZS. IDs: UUID strings. Phones: normalized E.164 (`+2557XXXXXXXX`); input accepts `07..`, `2557..`, `+2557..`.

## Sessions & cookies
Opaque token, httpOnly, SameSite=Lax, Path=/, Secure when ENV=prod. **One cookie per audience** so a tester can be renter + landlord in one browser:
- renter → `tms_r`; org user (owner/manager) → `tms_o`; platform admin → `tms_a`.
Renter routes read `tms_r`, org routes read `tms_o`, `/admin/*` reads `tms_a`. 401 = no/invalid session; 403 = wrong role; 404 = entity not in caller's org (never 403 for cross-org).

## Phase 1

### Auth
| Method/Path | Body → Response |
|---|---|
| `POST /auth/otp/send` | `{phone, purpose:"register"|"login"|"sign"}` → `202 {phone, resend_after_seconds:60}`. Rate limit 3/10min/phone → 429. Dev: code visible in `.dev/api.log` (`sms_body`). |
| `POST /auth/otp/verify` | `{phone, code, purpose}` → purpose `register`: `200 {otp_token}` (10-min Redis token); purpose `login`: sets `tms_r`, `200 {user}`. 5 attempts → 429. Wrong → 400. |
| `POST /auth/register/renter` | `{phone, otp_token, pin(4–6 digits), full_name}` → `201 {user}` + `tms_r`. Existing phone → 409. |
| `POST /auth/login` | `{phone, pin}` (renter → `tms_r`) **or** `{email, password}` (org user → `tms_o`; platform_admin → `tms_a`) → `200 {user, org:{id,name,slug,role}|null}`. Bad creds → 401. Rate limited. |
| `POST /auth/logout?audience=renter|org|admin` | → `204`, clears that cookie. |
| `GET /auth/me?audience=renter|org|admin` | → `200 {user, org:{...,role}|null}` or 401. |
| `POST /orgs` | `{org_name, owner_name, email, phone, password(min 8)}` → `201 {org, user}` + `tms_o`. Seeds payment_periods (30/90/180/365) + org_branding defaults. Sends verification email (dev: link logged to api.log). Dup email → 409. |
| `POST /auth/verify-email` | `{token}` → `200 {verified:true}`. Bad/expired token → 400. |
| `POST /auth/verify-email/resend` | (org session) → `202 {sent:true}`; already verified → `202 {sent:false, reason:"already_verified"}`. |
| `POST /auth/invite/accept` | `{token, password(min 8)}` → sets the invited staff member's password, marks their email verified, signs them in (`tms_o`) → `200 {user, org}`. Bad/expired token → 400. |

`user` shape: `{id, kind:"renter"|"org_user"|"platform_admin", phone, email, full_name, email_verified:boolean, status, created_at}`.

### Org (audience org; `tms_o`)
| `GET /org` | → `{id,name,slug,status,settings:{auto_approve_links:bool,due_day:int|null,grace_days:int,reminder_offsets_days:[7,0],unsigned_reminder_days:7,sms_language:"sw"|"en"},created_at}` |
| `PATCH /org` | `{name?, settings?(partial)}` → `200 {org}` (owner or manager). |
| `GET /org/members` | → `{items:[{id,user_id,email,full_name,role,status,created_at}]}` |
| `POST /org/members` | `{email, full_name, role:"org_owner"|"org_manager"}` → `201 {member, invite:{sent,link,expires_at}}`; owner only. Creates user with random temp password; invite link logged to api.log (dev, `email_link`) and spent via `POST /auth/invite/accept`. Dup → 409. |
| `DELETE /org/members/{id}` | → `204`; owner only; cannot remove last owner (409). |
| `GET /audit-log?entity_type=&actor=&from=&to=&cursor=&limit=` | → `{items:[{id,actor_user_id,actor_name,action,entity_type,entity_id,before,after,ip,at}],next_cursor}`. `cursor` = base64url of `at,id`; `limit` 1–200 (default 50); `from`/`to` RFC3339; `actor` = user id. `next_cursor` is `null` on the last page. |
| `GET /audit-log/{id}` | → one entry (same shape). An entry belonging to another org → **404**. |

Phase 1 audit actions: `auth.login`, `auth.login_failed`, `auth.logout`, `auth.otp_send`, `auth.register_renter`, `auth.verify_email`, `auth.invite_accept`, `org.create`, `org.update`, `org.member_invite`, `org.member_remove`. `auth.login_failed` and `auth.otp_send` carry no `org_id` (there is no session yet), so they are visible platform-side rather than in an org's log.

### Rate limits (Redis fixed window; Redis down → fail open, warning logged)
| Endpoint | Limit |
|---|---|
| `POST /auth/otp/send` | 3 / 10 min / phone, plus a 60 s resend cooldown |
| `POST /auth/otp/verify` | 5 / 10 min / phone |
| `POST /auth/login` | 10 / min / IP (reset on success) |
| `POST /orgs` | 5 / hour / IP |

Exceeding a limit → `429` with `Retry-After`.

### Health
`GET /healthz` → `{status,db,redis,minio}`.
