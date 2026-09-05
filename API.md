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

## Phase 2 — properties, units, QR, pricing, payment periods, vacancy, public

All org routes: audience org (`tms_o`), roles owner+manager unless noted. Cross-org id → 404. Every mutation audited.

### Payment periods (`/org/payment-periods`)
| `GET /org/payment-periods?include_inactive=true` | → `{items:[{id,label,days,is_recommended,sort_order,active,created_at}]}` sorted by sort_order, recommended first by default seed. |
| `POST /org/payment-periods` | `{label(1–40), days(int>0)}` → `201 {period}`; custom periods have `is_recommended:false`. Dup (label or days among active) → 409. |
| `PATCH /org/payment-periods/{id}` | `{label?, days?, sort_order?, active?}` → `200 {period}`. Changing `days` affects future contracts only. |
| `DELETE /org/payment-periods/{id}` | → `204` (soft: sets `active=false`). Cannot deactivate last active period → 409. |
| `POST /org/payment-periods/restore-recommended` | recreates any missing/deactivated recommended presets (30/90/180/365) → `200 {items}`. |

### Properties (`/properties`)
| `GET /properties?cursor=&limit=` | → `{items:[{id,name,location_text,lat,lng,notes,unit_counts:{total,vacant,occupied,maintenance,unlisted},created_at}],next_cursor}` |
| `POST /properties` | `{name(1–120), location_text?, lat?, lng?, notes?}` → `201 {property}` |
| `GET /properties/{id}` | → `{property}` (same shape + unit_counts) |
| `PATCH /properties/{id}` | partial → `200 {property}` |
| `DELETE /properties/{id}` | soft delete; 409 if any unit has an active/pending contract → `204` |
| `GET /properties/{id}/units` | → `{items:[unit]}` |
| `POST /properties/{id}/units` | `{name(1–60), price?:{amount(int>0), period_days(int>0, default 30)}, allowed_period_ids?:[uuid]}` → `201 {unit}`; generates `unit_code` (10-char Crockford base32, crypto-random) and creates first price_plan if price given. |
| `POST /properties/{id}/units/bulk` | `{names:[...] (1–200), price?}` → `201 {items}` |
| `GET /properties/{id}/qr-sheet` | → `{items:[{unit_id,unit_name,unit_code,scan_url,png_url}]}` presigned PNG URLs (15-min) for a printable sheet; generates any missing PNGs. |

### Units (`/units`)
`unit` shape: `{id,org_id,property_id,property_name,name,unit_code,status:"vacant"|"occupied"|"unlisted"|"maintenance",status_override:bool,allowed_period_ids:[uuid]|null,current_price:{id,amount,currency:"TZS",period_days,effective_from}|null,scan_url,vacant_since|null,created_at,updated_at}`
| `GET /units?status=&property_id=&q=&cursor=&limit=` | vacancy board → `{items:[unit],next_cursor}` |
| `GET /units/{id}` | → `{unit}` |
| `PATCH /units/{id}` | `{name?, status?("vacant"|"unlisted"|"maintenance" — landlord override; "occupied" is derived and rejected 400), allowed_period_ids?:[uuid]|null}` → `200 {unit}` |
| `DELETE /units/{id}` | soft delete, 409 if active/pending contract → `204` |
| `POST /units/{id}/qr` | (re)generate PNG into `qrcodes` bucket, key `{org_id}/{unit_id}.png` → `200 {unit_code, scan_url, png_url(presigned 15-min)}`. `scan_url = {APP_BASE_URL}/enduser/u/{unit_code}`. |
| `GET /units/{id}/prices` | → `{items:[{id,amount,currency,period_days,effective_from,created_at,created_by_name}]}` newest first |
| `POST /units/{id}/prices` | `{amount(int>0), period_days(int>0), effective_from(date, default today)}` → `201 {price}` |
| `POST /units/bulk-price` | `{unit_ids:[uuid], mode:"percent"|"set", value(number), period_days?, effective_from?}` → `200 {items:[price]}`; percent rounds to whole TZS. |

### Public (no auth; rate-limited 60/min/IP)
| `GET /public/orgs/{slug}/branding` | → `{org:{id,name,slug}, display_name, logo_url|null, theme:{primary_color,font_id}}` |
| `GET /public/units/{unit_code}` | → `{org:{id,name,slug}, branding:{display_name,logo_url,theme}, property:{name,location_text}, unit:{id,name,status}, price:{amount,currency,period_days}|null, periods:[{id,label,days,is_recommended,amount}] (amount prorated = round(price.amount × days / period_days)), occupied:bool}`. Unknown/deleted/unlisted → 404. |

### Phase 2 notes (implementation-confirmed)

- **Response envelopes:** single-entity responses are wrapped — `{property}`, `{unit}`, `{price}`, `{period}`; list responses are `{items, next_cursor}` (`next_cursor` only on the cursor-paginated `GET /properties` and `GET /units`).
- **Pagination:** `limit` 1–200, default 50; `cursor` = base64url of `created_at,id` (same encoding as `/audit-log`), ordered `created_at DESC, id DESC`. `GET /properties/{id}/units` and `/qr-sheet` are not paginated and return at most 500 units.
- **`current_price`** is the newest `price_plan` with `effective_from <= today`; a future-dated price appears in the history but is not current. `currency` is always `TZS` (server-set, never taken from the client).
- **Money and period bounds** (every price-carrying field): `amount` is a whole number of TZS in `1 … 999,999,999,999`; `period_days` (a price basis or a payment period's `days`) is `1 … 3650`. Out of range → **400**. The public endpoint prorates `amount × days` in 64-bit integers, so the ceilings are the arithmetic's, not a policy.
- **`POST /units/bulk-price`**: `mode:"percent"` on a unit with no current price → **409** (nothing to scale); one unknown/foreign `unit_id` → **404** and no prices are written. Every unit is resolved and priced before anything is written and all rows go in one transaction, so a 404/409 anywhere in the batch leaves every unit untouched. `value` is `1 … 999,999,999,999` for `set` and `-100 (exclusive) … 1000` for `percent`; a percentage that would push a unit outside the amount range → **409**. `period_days` defaults to each unit's current basis (30 if it has none). Amounts round to whole TZS.
- **`GET /units?q=`** matches unit name or property name (ILIKE); `%` and `_` in the query are escaped, not treated as wildcards.
- **`PATCH /units/{id}`**: `allowed_period_ids` must all be payment periods of the caller's org (else 400); `null` clears the restriction (all org periods offered); `[]`/omitted behave as no restriction. Setting `unlisted`/`maintenance` sets `status_override:true`; `vacant` clears it; `occupied` → 400.
- **`DELETE /properties/{id}`** soft-deletes the property *and* its units (their QR codes stop resolving). 409 if any unit has an `active`/`pending_signature` contract; same rule for `DELETE /units/{id}`.
- **Payment periods:** `label` and `days` are unique among an org's **active** periods (enforced by partial unique indexes; violation → 409, label comparison case-insensitive). `restore-recommended` leaves an already-active preset untouched, reactivates a deactivated one, creates a missing one, and never touches custom periods — repeat calls are no-ops.
- **QR:** PNG is 512 px, medium error correction, stored at `qrcodes/{org_id}/{unit_id}.png` (overwritten on regeneration); `png_url` is a 15-minute presigned GET. With MinIO unreachable the QR routes answer **503**.
- **Public:** `{unit_code}` is matched case-insensitively. A unit whose org is `suspended` → 404, as for unlisted/deleted. Offered `periods` carry `amount: null` when the unit has no price. Rate limit 60/min/IP (`Retry-After` on 429).

Phase 2 audit actions: `property.create`, `property.update`, `property.delete`, `unit.create`, `unit.update`, `unit.delete`, `unit.qr_generate`, `price.create`, `price.bulk_update`, `payment_period.create`, `payment_period.update`, `payment_period.delete`, `payment_period.restore_recommended`.

## Phase 3 — renter onboarding, KYC, link requests

### Renter (audience renter, `tms_r`)
| `GET /me/profile` | → `{user:{id,phone,full_name,email}, profile:{full_name,nida_masked("••••••••1234"|null),next_of_kin_name,next_of_kin_phone,email,kyc_status:"none"|"submitted"|"verified",kyc_doc_uploaded:bool,updated_at}}` |
| `PUT /me/profile` | `{full_name(2–120), nida_number?(20 digits; omit/empty keeps existing), next_of_kin_name(2–120), next_of_kin_phone(phone), email?}` → `200 {profile}`; sets `kyc_status:"submitted"` when nida + next-of-kin present. Audited (NIDA never in before/after; log `nida_changed:true`). |
| `POST /me/profile/kyc-upload` | `{content_type:"image/jpeg"|"image/png", size_bytes(≤5MiB)}` → `200 {upload_url(presigned PUT 10-min), object_key, headers}`; then `POST /me/profile/kyc-upload/complete {object_key}` → verifies object exists + size/type, stores key, `200 {profile}`. |
| `GET /me/profile/kyc-doc` | renter or org user (org: `/renters/{id}/kyc-doc`) → `{url(presigned GET 5-min)}`; audited `kyc.view`. |
| `POST /units/{unit_code}/link` | `{payment_period_id, term_days(int>0), start_date(date ≥ today-7), accepted_terms:true}` → `201 {request:{id,unit:{id,name,property_name},org:{name,slug},status,payment_period:{id,label,days,amount},term_days,start_date,end_date,schedule_preview:{count,first_due,amount_first,amount_last,total},created_at}}`. Rules: unit must be listed and not occupied (occupied → 409 `unit_occupied` unless org setting `waitlist`, not in MVP → always 409); one pending request per renter per unit → 409; period must be offered for that unit; auto-approve org setting → immediately approved (contract creation lands in Phase 4; for Phase 3 auto-approve sets status approved and emits SMS). Audited. Requires `kyc_status != "none"` → else 412 `kyc_required`. |
| `GET /me/link-requests` | → `{items:[request]}` newest first (includes `rejection_reason`). |
| `DELETE /me/link-requests/{id}` | cancel own pending → `204`. |

### Landlord (audience org)
| `GET /link-requests?status=pending|approved|rejected|cancelled` | → `{items:[{id,unit:{id,name,property_name},renter:{user_id,full_name,phone,kyc_status},payment_period:{label,days,amount},term_days,start_date,end_date,status,created_at,decided_at,rejection_reason}],next_cursor}` |
| `GET /link-requests/{id}` | → request + `renter_profile` (masked NIDA, next of kin, kyc_doc available flag) |
| `POST /link-requests/{id}/approve` | → `200 {request}`; status→approved, unit stays vacant until contract activation (Phase 4 hooks contract creation here). Queues SMS `link_approved` to renter (notification_log row status `queued`; sender live Phase 6 — dev LogProvider may send immediately). Pending only → else 409. |
| `POST /link-requests/{id}/reject` | `{reason(1–200)}` → `200 {request}`; SMS `link_rejected`. |
| `GET /renters?q=&kyc_status=&cursor=` | directory: `{items:[{user_id,full_name,phone,email,kyc_status,units:[{unit_id,unit_name,property_name,link_status}],created_at}]}` — renters known to this org = any link request or contract with this org. |
| `GET /renters/{user_id}` | → `{renter, profile(masked), link_requests:[...], contracts:[]}`; 404 if renter has no relationship with this org. |
| `GET /renters/{user_id}/kyc-doc` | → `{url}`; audited `kyc.view`. |

### Notifications (Phase 3 minimum)
`notification_log` rows written by approve/reject with `dedupe_key = link_{approved|rejected}:{request_id}`; `notify.Enqueue` pushes to Redis list `sms:queue`; a minimal worker goroutine sends via `SMSProvider` and updates `status` (`sent`|`failed`) — full scheduler in Phase 6.

### Phase 3 notes (implementation-confirmed)

- **Error codes:** the errors API.md names by code carry that code in the problem document's `type` field (`unit_occupied`, `unit_unavailable`, `period_not_offered`, `duplicate_request`, `not_pending`, `kyc_required`); everything else keeps `type: "about:blank"`. Field-validation 400s are unchanged (`errors` object).
- **`kyc_status`:** the wire values are `none | submitted | verified | rejected`; the column stores `incomplete` for `none` (the Phase 1 schema default) and the API translates at the edge. `rejected` is reachable only by an operator setting it directly — no Phase 3 endpoint produces it, but the filter accepts it.
- **`PUT /me/profile`:** `next_of_kin_name` and `next_of_kin_phone` are **required** (a profile without them cannot reach `submitted`, which is the gate on linking). `nida_number` omitted or empty keeps the stored value — the client only ever holds the mask, so it cannot echo one back. `email` is optional and is written to the user record (renter_profiles has no email column). `nida_masked` is `null` until a number is stored. Setting the status never lowers a `verified` profile.
- **KYC objects:** bucket `kyc`, key `{user_id}/{uuid}.{jpg|png}`. `POST /me/profile/kyc-upload` presigns a PUT for 10 minutes and echoes the `Content-Type` header the client must send. `.../complete` requires the key to sit under the caller's own `{user_id}/` prefix (else 400), then StatObjects it: a missing object, a size over 5 MiB or a content type other than `image/jpeg`/`image/png` is a **400**, and the rejected object is deleted. `GET .../kyc-doc` presigns a GET for 5 minutes and is audited `kyc.view`; with no document uploaded it is **404**. With MinIO unreachable the KYC routes answer **503**.
- **`POST /units/{unit_code}/link`:** `{unit_code}` is matched case-insensitively. Unit state decides the answer — `vacant` proceeds, `occupied` → 409 `unit_occupied`, `maintenance` → 409 `unit_unavailable`, `unlisted` → **404** (as on the public endpoint: the sticker must read as an unknown code), a suspended org → 404. `start_date` is `today−7 … today+365` (the backstop lets a renter record a move-in that already happened; the ceiling stops a request reserving a unit indefinitely). `term_days` is `1 … 3650`. `accepted_terms` must be `true`; the acceptance time is stored as `accepted_terms_at`. A period that is inactive, belongs to another org, or is outside the unit's `allowed_period_ids` → 409 `period_not_offered`. The duplicate check is backed by a partial unique index on `(unit_id, renter_user_id) WHERE status = 'pending'`, so two concurrent taps both answer 409 rather than both inserting.
- **`schedule_preview`** is computed by `internal/contract.Generate`, the same pure function Phase 4 materialises into `payment_schedules`: one row per cadence interval across the term, last row truncated, each row prorated from the unit's price basis. It is omitted when the unit has no current price. `first_due` equals the start date in Phase 3 (`due_day` snapping is applied at contract creation, not at application time).
- **Approval** sets `status`/`decided_at`/`decided_by_user_id` and calls the `onLinkApproved` hook, which is a no-op in Phase 3. **The unit deliberately stays `vacant`**: it becomes `occupied` when a contract activates (Phase 4), so an approved-then-abandoned request cannot strand a unit. Auto-approve takes the identical path at creation time, hook and SMS included.
- **State machine:** approve/reject/cancel all require `pending` → otherwise **409** `not_pending`. Rejecting or cancelling frees the renter to apply for that unit again. Cancelling sends no SMS (the renter did it themselves). `DELETE /me/link-requests/{id}` is scoped to the caller's own user id — another renter's request is a **404**.
- **Pagination:** `GET /link-requests` and `GET /renters` use the shared cursor (`limit` 1–200 default 50; `cursor` = base64url of `created_at,id`, ordered DESC). `GET /link-requests` also accepts `unit_id` and `renter_user_id`. `GET /me/link-requests` returns `{items, next_cursor}` and takes the same parameters.
- **`GET /renters`:** a renter is visible to an org iff they have a link request or a contract with it; the directory is empty until someone applies. `q` matches account name, profile name or phone (ILIKE, wildcards escaped). `kyc_status` filters on the wire values. `GET /renters/{user_id}` returns `{renter, profile, link_requests, contracts:[]}` — `contracts` is present but empty until Phase 4.
- **Notifications:** `notification_log` gains `to_phone`, `body`, `error` and `attempts` columns so the worker sends from one row read. `notify.Queue` writes the row inside the caller's transaction (`ON CONFLICT (dedupe_key) DO NOTHING`, status `queued`) and the Redis `RPUSH` happens only after the commit, so a rolled-back decision sends nothing. `notify.RunWorker` (started in `cmd/api`) BRPOPs, sends via the configured `SMSProvider`, and records `sent` (+`provider_msg_id`) or `failed` (+`error`); one attempt, no backoff — the retrying pool is Phase 6. On startup it re-enqueues rows still `queued` after a minute, so losing Redis loses no message. A renter with no phone number on file is logged and skipped; the decision itself still stands.
- **Templates** are plain Go string substitution in `internal/notify/templates.go`, Swahili and English per `orgs.settings.sms_language` (unknown language falls back to Swahili).

Phase 3 audit actions: `renter_profile.update` (records `nida_changed`, never the number), `kyc.upload`, `kyc.view`, `link_request.create`, `link_request.cancel`, `link_request.approve`, `link_request.reject`.

## Phase 4 — contract templates, contracts, signing, schedules

### Templates (audience org)
| `GET /contract-templates` | → `{items:[{id,name,is_default,updated_at,created_at}]}` |
| `GET /contract-templates/{id}` | → `{template:{id,name,body_html,is_default,variables:[...],created_at,updated_at}}` |
| `POST /contract-templates` | `{name(1–80), body_html(≤200 KiB, sanitized server-side: allow p,br,h1-h3,ul,ol,li,strong,em,u,table,thead,tbody,tr,td,th,blockquote; strip everything else incl. attributes except `class`), is_default?}` → `201 {template}` |
| `PATCH /contract-templates/{id}` | partial → `200 {template}` (never affects existing contracts — snapshot rule) |
| `DELETE /contract-templates/{id}` | soft delete → `204`; the org's default → **409** `template_is_default` (promote another first) |
| `POST /contract-templates/{id}/preview` | `{sample?:bool}` → `{html, letterhead_url, logo_url, display_name, footer_text}` — variables resolved with sample values, images presigned 1 h |
Variables: `{{renter_name}} {{unit}} {{property}} {{rent}} {{start_date}} {{end_date}} {{payment_period}} {{org_name}} {{term_days}} {{due_day}}`. Org creation seeds one default template ("Standard tenancy agreement") — migration/seed in this phase for existing orgs too.

### Contracts
`contract` shape: `{id,unit:{id,name,property_name},renter:{user_id,full_name,phone},template_id,status:"draft"|"pending_signature"|"active"|"expiring"|"ended"|"terminated",rent_amount,rent_period_days,payment_period:{id,label,days},term_days,start_date,end_date,due_day,snapshot_hash,signatures:[{party,name,signed_at,method,phone_masked,has_image}],link_request_id,created_at,activated_at,terminated_at,termination_reason,schedules_summary:{count,total,next_due_date,next_due_amount,paid_count,overdue_count}}`
| `POST /contracts` (org) | `{unit_id, renter_user_id, template_id?(default), payment_period_id, term_days, start_date, due_day?, link_request_id?}` → `201 {contract}` status `pending_signature`: resolves variables, stores `terms_snapshot_html`, computes `snapshot_hash = sha256(terms_snapshot_html + "|" + unit_id + renter_user_id + rent_amount + rent_period_days + payment_period_days + term_days + start_date + end_date + due_day)`. Unit must be vacant/listed, renter known to org, no other active/pending contract on unit → 409. Queues SMS `contract_ready` ("Your contract for {unit} is ready to sign. Open {APP_BASE_URL}/enduser/contract/{id}"). |
| Link approval hook | `POST /link-requests/{id}/approve` creates the contract from the request (default template) in the same tx and returns `{request, contract}`. Auto-approve takes the identical path (its response stays `{request}` — the renter reads the contract from `/me/contracts`). **Backfill:** approving a request that is *already* `approved` and has no contract creates one and answers `200 {request, contract}`; an already-approved request that already has a contract stays a **409** `not_pending`. |
| `GET /contracts?status=&unit_id=&renter_user_id=&cursor=` (org) / `GET /me/contracts` (renter) | → `{items:[contract],next_cursor}` |
| `GET /contracts/{id}` | either party (`tms_o` **or** `tms_r`); org: any in org; renter: own only (else 404) → `{contract}` |
| `GET /contracts/{id}/document` | both parties → `{contract_id,status,org:{display_name,logo_url,letterhead_url,footer_text},parties:{landlord:{name},renter:{name,phone_masked}},terms_html,schedule:[{period_start,period_end,due_date,amount}],signatures:[{party,name,signed_at,method,phone_masked,signature_image_url}],snapshot_hash,generated_at}`. Schedule shown from `payment_schedules` when active, else generated preview. |
| `POST /contracts/{id}/sign/otp` (renter, own, status pending_signature, not yet signed) | → `202 {resend_after_seconds:60}`; OTP purpose `sign`, keyed to the contract (Redis key `otp:sign:{contract_id}:{phone}`); 3 / 10 min / contract plus the 60 s resend cooldown. Already signed → **409** `already_signed`; wrong status → 409 `not_signable`. |
| `POST /contracts/{id}/signature-upload` (renter) | `{content_type:"image/png", size_bytes ≤ 512 KiB}` → `{upload_url, object_key, headers}` (bucket `signatures`, key `{org_id}/{contract_id}/renter.png`). |
| `POST /contracts/{id}/sign` (renter) | `{otp_code, signature_object_key?}` → `200 {contract}`; refuses a second signature (**409** `already_signed`) before spending the code, rechecks the hash against the stored row (**409** `snapshot_mismatch`), verifies the OTP (wrong/expired → **400** `{errors:{otp_code}}`), then inserts the `contract_signatures` row (party renter, method `drawn` if a key was given else `otp_accept`, otp_ref, ip, ua, snapshot_hash). No SMS to the landlord — they use the dashboard. |
| `POST /contracts/{id}/activate` (org) | rechecks the snapshot hash (**409** `snapshot_mismatch`), requires a renter signature → else **412** `renter_signature_required`; inserts the landlord signature row (method `otp_accept`, user = session), generates all `payment_schedules` via `contract.Generate`, status `active`, `activated_at`, unit → `occupied` (clears override; the link request stays `approved`). Queues SMS `welcome`. Wrong status → 409 `not_pending_signature`. |
| `POST /contracts/{id}/terminate` (org) | `{reason(1–200), effective_date?(default today)}` → `200 {contract}`: status `terminated`, `terminated_at`, `termination_reason`, remaining `pending`/`partial`/`overdue` schedules with `period_start > effective_date` → `waived`; unit → vacant unless another contract still runs on it; SMS `contract_terminated`. Allowed from pending_signature/active/expiring, else **409** `not_terminable`. Pending-signature cancel = same endpoint. |
| `GET /contracts/{id}/verify` | → `{valid:bool, computed_hash, stored_hash, signatures:[...]}` |
| Landlord-recorded renter (FLOWS 3.6) | `POST /contracts/{id}/activate` with body `{landlord_recorded:true, reason(1–200)}` when the renter has no signature → allowed, audit action `contract.activate_landlord_recorded`, landlord signature row carries method `landlord_recorded`; no renter signature row. A missing `reason` is a **400**. |
| Expiring job | `internal/contract.RunLifecycle`: `active` with `end_date - 30d <= today < end_date` → `expiring`; `active`/`expiring` with `end_date <= today` → `ended`, unit → vacant (if no other live contract). Runs once at API startup and hourly thereafter; also `POST /admin/jobs/contract-lifecycle` (platform admin, `tms_a`) → `200 {expiring, ended, units_freed}`. Idempotent. |

### Schedules (read; payments in Phase 5)
| `GET /contracts/{id}/schedules` | → `{items:[{id,period_start,period_end,due_date,amount,status,paid_amount}]}` |
| `GET /me/schedules` (renter) | → `{items:[{id,period_start,period_end,due_date,amount,status,paid_amount,contract:{id,unit_name,property_name,org_name,status}}], next_due:{…}|null}` — `next_due` is the first unsettled row on a live contract (the same object, not a copy). |

### Branding (audience org; needed for contract documents — full theming UI in Phase 7)
| `GET /org/branding` | → `{display_name, logo_url|null, letterhead_url|null, theme:{primary_color,font_id}, dashboard_prefs:{}, document_footer_text|null}` (URLs presigned 1h) |
| `PUT /org/branding` | `{display_name?, theme?:{primary_color(hex), font_id(bricolage|archivo|instrument|hanken)}, dashboard_prefs?, document_footer_text?(≤500)}` → `200 {branding}` |
| `POST /org/branding/logo` / `POST /org/branding/letterhead` | `{content_type:image/png|image/jpeg, size_bytes ≤ 2 MiB}` → `{upload_url, object_key, headers}` (bucket `branding`, key `{org_id}/logo.{ext}` / `{org_id}/letterhead.{ext}`); then `POST /org/branding/{logo|letterhead}/complete {object_key}` → `{branding}`. `DELETE /org/branding/{logo|letterhead}` → `{branding}`. |
Public branding endpoint (`/public/orgs/{slug}/branding`, `/public/units/{code}`) returns `logo_url` presigned when set.

### Phase 4 notes (implementation-confirmed)

- **Error codes** carried in the problem document's `type` (everything else stays `about:blank`): `unit_occupied`, `unit_unavailable`, `unit_not_priced`, `contract_exists`, `period_not_offered`, `template_not_found`, `template_is_default`, `not_signable`, `already_signed`, `snapshot_mismatch`, `not_pending_signature`, `not_terminable`, `not_pending`, and the 412 `renter_signature_required`.
- **Audiences:** the four contract *reads* — `GET /contracts/{id}`, `/document`, `/verify`, `/schedules` — accept either an org (`tms_o`) or a renter (`tms_r`) cookie, because one document has two readers (SPEC §5.5). Each scopes its query by whoever arrived (org id for the landlord, user id for the renter), so the other party's contract is a **404**. Writes stay one-sided: create/activate/terminate are org-only, sign/sign-otp/signature-upload are renter-only.
- **`POST /contracts` rules,** in the order they are checked: unit in the caller's org (404) → unit `occupied` (409 `unit_occupied`) / `unlisted` or `maintenance` (409 `unit_unavailable`) → unit has a current price (409 `unit_not_priced`) → no other `pending_signature`/`active`/`expiring` contract on the unit (409 `contract_exists`, also enforced by a partial unique index on `unit_id`, so a race loses with the same answer) → renter known to the org, i.e. has a link request or contract with it (404) → period active, in the org, and inside the unit's `allowed_period_ids` (409 `period_not_offered`) → a template (the named one, else the org default; 409 `template_not_found`). `term_days` is 1…3650; `due_day` 1…31 (migration 000005 widens the column check from 28 — `contract.Generate` clamps a 31 to the length of the month).
- **`due_day`** falls back to the org setting `settings.due_day` when the request omits it, and stays NULL when neither is set (each row then falls due on the day its period starts).
- **Snapshot:** `terms_snapshot_html` is the template body sanitized again and rendered with `{{renter_name}} {{unit}} {{property}} {{rent}} {{start_date}} {{end_date}} {{payment_period}} {{org_name}} {{term_days}} {{due_day}}`; **values are HTML-escaped, the body is not** — a renter called `Asha <script>` becomes text. `{{rent}}` is formatted `TZS 250,000`, `{{payment_period}}` is `Monthly (30 days)`, `{{due_day}}` is a phrase rather than a bare number (`day 5`, or `the first day` when the contract has no due day, so the clause still reads), an unknown variable resolves to blank. `snapshot_hash` is the hex SHA-256 of `terms_snapshot_html|unit_id|renter_user_id|rent_amount|rent_period_days|payment_period_days|term_days|start_date|end_date|due_day` (a NULL due day contributes an empty field). `GET /verify` recomputes it from the stored row, so editing `terms_snapshot_html` or any commercial column in SQL flips `valid` to false.
- **Approval creates the contract, or the approval fails.** If the contract cannot be written (unpriced unit, no template, unit taken) the approval is refused whole with that contract error, and nothing is committed — a renter is never told "approved" with nothing to sign. The same holds for auto-approve at application time.
- **Backfill:** `POST /link-requests/{id}/approve` on an already-`approved` request with no contract creates one (`200 {request, contract}`); with a contract it is a `409 not_pending`. Idempotence is backed by `contracts.link_request_id`.
- **Signing:** the OTP is keyed to the contract, so two contracts awaiting signature never share or cancel each other's code; the SMS names the unit. The signature row records `otp_ref`, the caller's IP and user agent, and the hash that was on screen. A drawn signature is optional: `POST /signature-upload` presigns a 10-minute PUT for `signatures/{org_id}/{contract_id}/renter.png` (image/png, ≤512 KiB) and `POST /sign` accepts exactly that key, refusing (400) any other key or a key with no object behind it; supplying one sets `method: "drawn"`. The object is StatObject'd at signing (a presigned PUT enforces neither), and anything that is not `image/png` or is over 512 KiB is deleted and refused **400**. Signing attempts are rate-limited like the login OTP (5 / 10 min / contract). `contract_signatures` is append-only with a unique index on `(contract_id, party)`.
- **Activation** writes every `payment_schedules` row for the whole term in one transaction: one row per cadence interval, the last truncated, each amount prorated from the contract's own snapshotted rent basis (`rent × days / rent_period_days`, rounded half away from zero). 180 days at a 45-day cadence → four 45-day rows; 100 days at 30 → 30/30/30/10. `paid_amount` starts at 0 (Phase 5 writes it).
- **`GET /contracts/{id}/document`** returns `org:{display_name,logo_url,letterhead_url,footer_text,theme}` (images presigned 1 h), `terms_html`, `parties`, `signatures[]` (with `signature_image_url` presigned 1 h when a drawn image exists), `snapshot_hash` and `generated_at`. `schedule` is the stored `payment_schedules` once they exist, and the same generator's preview before activation — the renter must see the money before agreeing to it.
- **Notifications:** kinds `contract_ready`, `welcome`, `contract_terminated`, dedupe key `{kind}:{contract_id}`, Swahili/English per `orgs.settings.sms_language`. An approval therefore writes **two** rows: `link_approved` and `contract_ready`.
- **Branding:** presigned reads last 1 hour (a document is read, printed and re-read). Upload keys are fixed per org and asset — `branding/{org_id}/logo.{png|jpg}` and `.../letterhead.{png|jpg}` — so a re-upload replaces the image; `.../complete` StatObjects what landed and rejects a wrong type or a file over 2 MiB (deleting it). `DELETE` clears the reference and removes the object. `theme.primary_color` must be a 6-digit hex (`#1B4DB1`); `theme.font_id` is one of `bricolage|archivo|instrument|hanken`. An explicit `document_footer_text: null` clears the footer. With MinIO unreachable the upload/delete routes answer **503**; a read simply omits the URL.
- **Templates:** an org has at most one default (partial unique index). Promoting one demotes the incumbent; clearing the only default is a **400** (promote another instead). Bodies are sanitized on write *and* again on render, so a body stored before a policy change cannot escape the current allowlist; a body with nothing left after sanitizing is a **400**. Editing a template never touches an existing contract — the contract carries its own snapshot.
- **Seeding:** every org gets one default template, "Standard tenancy agreement". New orgs get it at `POST /orgs`; orgs that already existed get a byte-identical copy from migration 000005 (idempotent — it inserts only where an org has no live template). `internal/contract.DefaultTemplateBody` and the migration are kept in step by a test.

Phase 4 audit actions: `contract_template.create`, `contract_template.update`, `contract_template.delete`, `contract.create`, `contract.sign`, `contract.activate`, `contract.activate_landlord_recorded`, `contract.terminate`, `contract.lifecycle_run`, `org.branding_update`, `org.branding_asset`. Requesting a signing code reuses `auth.otp_send` with entity type `contract`.

## Phase 5 — offline payments & statuses

`schedule` shape: `{id,contract_id,period_start,period_end,due_date,amount,paid_amount,status:"pending"|"paid"|"partial"|"overdue"|"waived",days_overdue,contract:{id,unit_name,property_name,renter_name,renter_user_id}}`
`payment` shape: `{id,contract_id,schedule_id,amount,method:"cash"|"bank_transfer"|"mobile_money_manual",reference,paid_at,note,status:"recorded"|"reversed",recorded_by:{user_id,name},reversed_at,reversal_reason,applied:[{schedule_id,amount}],created_at}`

### Landlord (audience org)
| `GET /schedules?status=&contract_id=&renter_user_id=&due_from=&due_to=&cursor=&limit=` | → `{items:[schedule],next_cursor}` (`status=overdue` = overdue view) |
| `POST /payments` | `{contract_id, schedule_id?, amount(int>0), method, reference?(≤80), paid_at(datetime, default now, not future >1d), note?(≤500), allow_overpay_rollover?:bool}` → `201 {payment, schedules:[affected schedule]}`. Allocation: target = `schedule_id` or the earliest unpaid (`pending|partial|overdue`) schedule of the contract. Apply `amount` to target: remaining = amount − (target.amount − target.paid_amount). If remaining > 0: when `allow_overpay_rollover` true → apply to following unpaid schedules in order (recorded in `applied[]`); when false → **409 `overpay_confirm_required`** with `{detail, excess, next_schedule:{...}}` so the UI can prompt (FLOWS 7 "overpayment → applied to next schedule (confirm prompt)"). Any leftover after all schedules are paid → 409 `exceeds_contract_balance`. Status flip: paid_amount ≥ amount → `paid`; 0 < paid < amount → `partial`; contract must be `active`/`expiring` → else 409. Audited `payment.record`. Queues SMS `thank_you` (amount received + next due date/amount or "all paid"). |
| `POST /payments/{id}/reverse` | `{reason(1–200)}` → `200 {payment, schedules}`; status `reversed`, un-applies every `applied[]` amount, recomputes schedule statuses (pending/partial and overdue if due_date < today); audited `payment.reverse`; 409 if already reversed. |
| `GET /payments?contract_id=&renter_user_id=&method=&from=&to=&cursor=` | → `{items:[payment],next_cursor}` |
| `GET /payments/{id}` | → `{payment}` |
| `POST /admin/jobs/overdue` (platform admin) + internal ticker (hourly) | flips `pending|partial` with `due_date + grace_days < today` → `overdue` (grace from org settings); `overdue` that become paid handled by record. Returns `{flipped:n}`. Renter/landlord reads also apply an on-demand check (derive `overdue` at read time if due passed — simplest: the read endpoints call the flip for that org first). |
| `GET /org/bank-account` / `PUT /org/bank-account` | `{bank_name, account_name, account_number, instructions(≤300)}` stored in `orgs.settings.bank_account` — shown to renters on the payment screen (FLOWS 7). |

### Renter (audience renter)
| `GET /me/schedules` (extend) | `{items:[schedule], next_due:schedule|null, overdue_total, bank_account:{...}|null}` |
| `GET /me/payments` | → `{items:[payment]}` (own contracts only; `recorded_by` name only) |

Schedules for a renter show `status` chip: paid (stamp), pending (pencil), overdue (stamp red), partial (pencil + "TZS x of y").

### Phase 5 notes (implementation-confirmed)

- **Error codes** carried in the problem document's `type`: `overpay_confirm_required`, `exceeds_contract_balance`, `schedule_paid`, `contract_not_active`, `already_reversed`. `overpay_confirm_required` is the one problem document with extra members — `excess` (int TZS) and `next_schedule:{id,due_date,amount,paid_amount,status}` — so the UI can raise the confirm prompt without a second round trip.
- **`POST /payments` rules,** in the order they are checked: field validation (400) → contract in the caller's org (404) → contract `active`/`expiring` (409 `contract_not_active`) → an explicit `schedule_id` belongs to that contract (404) → the target owes something (409 `schedule_paid`) → allocation. With no `schedule_id` the target is the earliest schedule that still owes something; on a fully settled contract that is a 409 `exceeds_contract_balance`. `method` is `cash|bank_transfer|mobile_money_manual` (`gateway` exists in the column for the post-MVP seam and is not accepted). `amount` is `1 … 999,999,999,999`; `paid_at` is RFC3339, defaults to now and may not be more than 24 h ahead. The contract's schedules are `SELECT … FOR UPDATE`-locked for the whole transaction, so two clerks recording at once cannot both allocate against the same balance.
- **Allocation** is `internal/payment.Allocate`, a pure function table-tested apart from the database. It applies to the target first, then walks *forward* through the schedules after it, skipping settled and waived rows; `applied[]` lists every row reached, in due order. An overpayment on the last unpaid schedule is `exceeds_contract_balance` rather than `overpay_confirm_required` — there is nothing to prompt about. Recording never sets `overdue`: a covered row becomes `paid`, a part-covered one `partial` (the sweep re-flags it if it is still late). A `waived` row is never revived by a payment.
- **`POST /payments/{id}/reverse`** un-applies exactly the stored `applied[]` amounts and recomputes each schedule from scratch — `paid` if still covered, else `overdue` when `due_date + grace_days < today`, else `partial`/`pending`. The payment row stays, `status: "reversed"` with `reversed_at`, `reversal_reason` and `reversed_by_user_id`; a correction is part of the record, not a deletion. A second reversal → 409 `already_reversed`.
- **Overdue:** `internal/payment.FlipOverdue` moves `pending`/`partial` rows with `due_date + grace_days < CURRENT_DATE` to `overdue`, taking the grace period from each org's own `settings.grace_days` (default 0 when the key is absent; new orgs seed 3). It runs once at API startup and hourly after that, on demand via `POST /admin/jobs/overdue` (platform admin, `tms_a`) → `{flipped}`, and org-scoped ahead of `GET /schedules` and `GET /me/schedules` so neither read can show a stale `pending`. It is idempotent. The on-demand read sweeps are not audited (they are a read's own housekeeping); the admin job writes `payment.overdue_run`.
- **`days_overdue`** is whole days from `due_date` to today, floored at 0, and always 0 on a `paid` or `waived` row — it is derived at read time, not stored.
- **Pagination:** `GET /payments` and `GET /me/payments` page by `(paid_at, id)` descending; `GET /schedules` pages by `(due_date, id)` **ascending** — the board reads forward, because the next thing owed is the first thing to show. Both use the shared cursor encoding (base64url of `timestamp,id`), `limit` 1–200 default 50. `GET /me/payments` returns `{items, next_cursor}` like the other listings.
- **`applied[]` ordering:** allocations are written inside one transaction and share a `created_at` to the microsecond, so they are read back ordered by the schedule's due date — the order the money actually walked.
- **`payment` shape** also carries `unit_name`, `property_name` and `renter_name` so a history table needs no second request. `recorded_by` is `{user_id, name}` for the landlord and `{name}` only for the renter (`GET /me/payments`).
- **`GET /schedules`** filters: `status` (one of `pending|paid|partial|overdue|waived`, anything else 400), `contract_id`, `renter_user_id`, `due_from`, `due_to` (dates, inclusive). `GET /payments` filters: `contract_id`, `renter_user_id`, `method`, `from`, `to` (RFC3339 or a bare date). `GET /payments/{id}` is org-only; a renter reads their own history through `GET /me/payments`.
- **Bank account:** `GET`/`PUT /org/bank-account` wrap the object — `{bank_account: {...}|null}` — and `null` until it is set. `bank_name`, `account_name` and `account_number` are required (≤120 chars each), `instructions` optional (≤300). It is stored inside `orgs.settings.bank_account`, so `PATCH /org` round-trips it untouched and never clears it. Audited `org.bank_account_update`.
- **`GET /me/schedules`** adds `days_overdue` per row, `overdue_total` (the sum still owed on `overdue` rows) and `bank_account`. The account shown is the one belonging to the org behind `next_due`; a renter with nothing outstanding is not being asked for money, so it is `null`. A renter renting from several orgs gets one sweep per org before the rows are rendered.
- **Notifications:** kind `thank_you`, dedupe key `thank_you:{payment_id}`, Swahili/English per `orgs.settings.sms_language`. It names the amount received and the next instalment's amount and date, or says every payment is up to date when nothing is left.
- **Schema:** migration `000007_payments` adds `payments.reversed_at/reversal_reason/reversed_by_user_id` (with a check constraint that a reversal carries both timestamp and reason) and the `payment_allocations` table (`org_id, payment_id, schedule_id, amount`, unique per `(payment_id, schedule_id)`). `notification_log` already accepted `thank_you`; `orgs.settings.bank_account` is JSON inside the existing column, so neither needed a change.

Phase 5 audit actions: `payment.record`, `payment.reverse`, `payment.overdue_run`, `org.bank_account_update`.

## Phase 6 — notifications end-to-end

### Org settings (audience org)
| `GET /org/notification-settings` | → `{sender_name(≤11, null = platform default), language:"sw"|"en", send_hour_local:9, kinds:{reminder_7d:{enabled,offset_days:7},reminder_due:{enabled},overdue_daily:{enabled},thank_you:{enabled},unsigned_reminder:{enabled,after_days:7}}, templates:{[kind]:{sw:string,en:string}|null}}` (null template = platform default). Variables: `{{name}} {{amount}} {{due_date}} {{property}} {{unit}} {{org}} {{next_due_date}} {{link}}`. |
| `PUT /org/notification-settings` | partial merge → `200 {settings}` (the same shape as the GET, not a `settings` wrapper); validates hour 0–23, offsets and `after_days` 0–30, sender name ≤ 11, template length ≤ 320 chars, unknown variables rejected 400. A `null` template clears the override. Stored in `orgs.settings.notifications`; `language` is the existing `orgs.settings.sms_language` surfaced under the name API.md gives it, so PATCH `/org` and this endpoint agree. Audited `org.notification_settings_update`. |
| `POST /notifications/custom` | `{recipients:"all_active"|"selected", renter_user_ids?:[uuid ≤500], body(1–320)}` → `202 {batch_id, queued:n, skipped:n}`; body variables limited to `{{name}} {{unit}} {{property}} {{org}}`; a renter with no phone on file, and a `selected` id this org does not know, are counted in `skipped` (never a 404, so a broadcast cannot probe another org's directory); only renters with an active/expiring contract in the org (for `all_active`) or related renters (for `selected`); dedupe_key `custom:{batch_id}:{user_id}`; owner + manager; audited `notification.custom` with count + body. Rate limit 10 batches/hour/org. |
| `GET /notifications/log?kind=&status=&user_id=&from=&to=&limit=&cursor=` | → `{items:[{id,kind,to_phone(full — the landlord already holds the renter's number),renter_name,body,status:"queued"|"sending"|"sent"|"failed",provider_msg_id,error,attempts,batch_id,created_at,sent_at}],next_cursor}`; newest first, cursor over `(created_at,id)`. |
| `POST /notifications/log/{id}/retry` | failed → re-queue → `202 {id,status:"queued"}`; any other status → 409 `not_failed`; another org's row → 404. Audited `notification.retry`. |

### Scheduler (system)
`internal/notify/scheduler.go` — 5-min ticker (and `POST /admin/jobs/notifications {date?, force_hour?:bool}` → `200 {queued:{kind:n}}`, platform-admin only, audited `notification.scheduler_run`):
- For each org (with settings): local time zone Africa/Dar_es_Salaam; only enqueue kinds whose `send_hour_local` has been reached today.
- `reminder_7d`: schedules `pending|partial` with `due_date = today + offset_days` → dedupe `reminder_7d:{schedule_id}:{date}`.
- `reminder_due`: `due_date = today` → `reminder_due:{schedule_id}:{date}`.
- `overdue_daily`: status `overdue` → `overdue_daily:{schedule_id}:{date}` (daily until paid/waived).
- `unsigned_reminder`: contracts `pending_signature` without renter signature, `created_at + after_days <= now` → `unsigned:{contract_id}:{date}`.
- `thank_you` stays event-driven (Phase 5). Templates: org override else platform default, SW/EN.
Worker pool: N=3 workers (`NOTIFY_WORKERS`), atomic claim (`UPDATE notification_log SET status='sending' WHERE id=$1 AND status='queued' RETURNING`), Beem call with 3 attempts on the 1s/5s/25s schedule — with three attempts the pauses used are 1s and 5s — then `sent` (+`provider_msg_id`, `attempts`) or `failed` (+`error`, `attempts`). Redis loss safe: the startup sweep re-enqueues rows still `queued` after a minute and returns rows left `sending` for over five minutes to the queue.
Beem provider: POST `https://apisms.beem.africa/v1/send` basic auth (`api_key:secret_key`), body `{source_addr, schedule_time:"", encoding:0, message, recipients:[{recipient_id:1, dest_addr:"255…"}]}`, 10s timeout; parse `request_id`; non-2xx or `successful:false` → error; `ENV=prod` requires creds. `source_addr` = the org's `sender_name` if set, else `BEEM_SENDER_ID`, else `INFO`.

**Rendering:** every kind — `link_approved`, `link_rejected`, `contract_ready`, `welcome`, `contract_terminated`, `thank_you`, `reminder_7d`, `reminder_due`, `overdue_daily`, `unsigned_reminder`, `custom` — goes through one `notify.Render(kind, lang, vars, orgOverrides)`. Placeholders are `{{…}}`. The eight variables above are the ones an org may write; the platform defaults additionally use `{{reason}}` (rejection, termination), `{{start_date}}` (welcome) and `{{next_amount}}` (thank-you), which an org override cannot name — an override of those kinds simply does without them. `thank_you` keeps its two platform wordings (with and without a next instalment); an org override of `thank_you` is used for both.

**Schema:** migration `000008_notifications` adds kind `unsigned_reminder` and status `sending` to `notification_log`, a `batch_id` column for custom broadcasts, an `(org_id, created_at DESC, id DESC)` index for the log listing and a partial index on claimed rows for the startup sweep.

Phase 6 audit actions: `org.notification_settings_update`, `notification.custom`, `notification.retry`, `notification.scheduler_run`.

## Phase 7 — reports, dashboard prefs, admin, PWA

### Reports (audience org)
| `GET /reports/summary?period=month|YYYY-MM` | → `{assets:{properties,units,occupied,vacant,maintenance,unlisted,occupancy_rate(0–1)}, renters:{active}, contracts:{active,expiring,pending_signature}, period:{from,to,expected,collected,outstanding,overdue_count,overdue_amount}, vacant_units:[{unit_id,name,property_name,days_vacant}] (≤20, longest first)}`. Expected = schedules with due_date in period (excl. waived); collected = non-reversed payments with paid_at in period. |
| `GET /reports/payment-status?status=&property_id=&format=json|csv` | per renter with active/expiring contract: `{items:[{renter_user_id,renter_name,phone,unit_name,property_name,contract_id,status:"paid"|"pending"|"overdue"|"partial",next_due_date,next_due_amount,outstanding,overdue_amount,last_payment_at}]}`; `format=csv` → `text/csv` attachment `payment-status-{date}.csv`. Status = worst of the renter's unsettled schedules (overdue > partial > pending; paid when none unsettled). |
| `GET /reports/collections?from&to&group=day|week|month` | → `{buckets:[{start,expected,collected}], totals:{expected,collected}}`. Defaults: `group=month`, and the twelve months ending today. `from`/`to` are `YYYY-MM-DD`, inclusive. Every bucket in the range is emitted, zeros included; `start` is the bucket's first day. At most 400 buckets — a longer range with a finer grouping is a 400 on `group`. |
| `GET /reports/audit?...` | (already `GET /audit-log`) |

Notes fixed in Phase 7 (implementation detail, same for all three reports):

- **Occupancy** = `occupied / (occupied + vacant)`. `unlisted` and `maintenance` units are neither let nor a vacancy the landlord is failing to fill, so they are outside the ratio; `occupancy_rate` is `0` when nothing is lettable.
- **Outstanding** = `sum(amount − paid_amount)` over the `pending`/`partial`/`overdue` schedules of the window; `overdue_amount` is the same sum restricted to `overdue`.
- **`renters.active`** counts distinct renters holding an `active`/`expiring` contract.
- **`days_vacant`** counts from the `end_date` of the unit's last `ended`/`terminated` contract, or from the unit's creation when it has never been let.
- **Calendar boundaries** are Dar es Salaam wall clock: `period=month` is the month it is *there*, and the `paid_at` window is `[first day 00:00 EAT, last day + 1 00:00 EAT)`. Collections buckets truncate `paid_at` in EAT too. `due_date` is already a calendar date and needs no shift.
- Every report runs the org-scoped overdue flip first, as `GET /schedules` does, so a status is never quoted stale.
- `status=` on payment-status filters the *derived* status; an unknown value is a 400.

### Dashboard prefs
`PUT /org/branding {dashboard_prefs:{cards:["assets","renters","payment_status","collections","link_requests","overdue","expenses","revenue","net_income"], layout:"grid"|"list"}}` — free JSON validated to known card ids; frontend orders cards by it.

Validation (400 with `errors.dashboard_prefs.*`): `cards` must be an array of ids drawn from that exact list, with no repeats; `layout` must be `grid` (default) or `list`; any other key in the object is refused rather than silently dropped. The stored blob is the canonical `{cards, layout}` shape, and `cards` keeps the order it was sent in — that order is the dashboard's.

### Platform admin (audience admin `tms_a`)
| `GET /admin/orgs?q=&status=&cursor=` | → `{items:[{id,name,slug,status,owner:{name,email},counts:{properties,units,renters,active_contracts},sms:{sent_30d,failed_30d},created_at}],next_cursor}` |
| `GET /admin/orgs/{id}` | detail incl. settings summary, members |
| `POST /admin/orgs/{id}/suspend {reason}` / `POST /admin/orgs/{id}/activate` | → `{org}`; suspended org: org users get 403 `org_suspended` on every org route, public unit endpoints 404, scheduler skips. Audited (org_id set, actor admin). |
| `GET /admin/metrics` | → `{orgs:{total,active,suspended},renters:{total},units:{total,occupied},contracts:{active},sms:{sent_24h,failed_24h,queued},payments:{recorded_30d,amount_30d},db:{ok},redis:{ok},minio:{ok}}` |
| `GET /admin/audit-log?org_id=&actor=&entity_type=&entity_id=&q=&from=&to=&cursor=` | cross-org audit search |
| `GET /admin/jobs` | `{items:[{name,action,description,runnable,last_run_at,last_result}]}` — the three schedulers (`contract-lifecycle`, `overdue`, `notifications`), each with its last run read from the audit trail; existing `POST /admin/jobs/{contract-lifecycle|overdue|notifications}` |

Notes fixed in Phase 7:

- `GET /admin/orgs` also accepts `limit` (1–100, default 25) and pages by `(created_at, id)` descending with the same opaque cursor as the other listings. `q` matches the org name or slug, case-insensitively. Rows carry `suspended_at` and `suspended_reason` as well.
- `POST /admin/orgs/{id}/suspend` **requires** a non-empty `reason` (≤500 chars); it is stored on the org and appears in the audit `after`. Activation clears both. An unknown or malformed org id is a 404.
- **Suspension enforcement**: `RequireOrg` (and the org half of the contract-party routes) answers 403 with `type: "org_suspended"` for every org route. Backed by a Redis flag `org:suspended:{org_id}` written on suspend/activate, with a `SELECT status FROM orgs` fallback (cached 5 min) whenever the flag is cold — Redis is ephemeral, so the database always decides. Renters and platform admins are unaffected; a suspended landlord's renter can still read their own contracts and pay. Login itself still succeeds — the lockout is on the org routes, so the operator's own tooling can tell "wrong password" from "org closed".
- **Notifications**: `ClaimNotification` will not claim a suspended org's message. The row stays `queued` (nothing is marked `failed`), so reactivating the org releases the backlog rather than leaving a trail of errors. The scheduler already skips suspended orgs, and the public endpoints already 404 for them.
- `GET /admin/audit-log` also accepts `limit` (1–200, default 50); `q` is a case-insensitive substring of `action` or `entity_type`. Rows carry `org_name` beside `org_id`.
- `GET /admin/metrics` reports `db`/`redis`/`minio` as `{ok: bool}` from the same probes as `/healthz`.
- **No `job_runs` table.** `GET /admin/jobs` derives `last_run_at`/`last_result` from the newest audit row per job action (`contract.lifecycle_run`, `payment.overdue_run`, `notification.scheduler_run`), which the jobs already write. One store, one truth.

### PWA
Each app: `public/manifest.webmanifest` (name per app, `start_url` = basePath, display standalone, theme_color from core palette, icons 192/512 generated PNG), `<link rel=manifest>` in layout, service worker `public/sw.js` registered client-side: cache-first for app shell (`/_next/static/*`, manifest, icons), network-only for `/api/*` and presigned bucket paths, navigation fallback = cached shell; no offline writes. Registered only in production builds or when `NEXT_PUBLIC_ENABLE_SW=1` (avoid HMR interference in dev).

## Part 2 (Phases 9–15) — planned contract

Everything below is **planned** until its phase ships; a row becomes live when the phase's `PROGRESS.md` entry lands, and the phase heading drops the "planned" marker. Plan and phase order in [PLAN2.md](PLAN2.md); semantics in SPEC §2.0, §3.2, §5.11–5.13, §6, §7.

### Phase 9 — foundations + end-to-end fixes (planned)

| `POST /org/payment-periods/{id}/recommend` | (no body) → `200 {period}` — moves the single "Recommended" badge to this period and clears it from every other one in the org, in one transaction. Audience org (`tms_o`), owner + manager. An inactive or soft-deleted period → 409 `period_inactive`; another org's id → 404. Audited `payment_period.recommend` (`before`/`after` name the period that lost and the one that gained the badge). |
| `GET /org/payment-periods` | unchanged shape, with a new guarantee: **at most one item has `is_recommended: true`** (partial unique index `payment_periods (org_id) WHERE is_recommended AND deleted_at IS NULL`). Ordering stays recommended first, then `sort_order`. `POST /org/payment-periods/restore-recommended` restores any missing seeded presets (30/90/180/365) unbadged; if the org has no recommended period at all, Monthly is badged so there is never zero. |
| `GET /public/units/{unit_code}` | unchanged shape; `periods[]` is ordered recommended-first, and the badge now marks exactly one entry. |
| `GET /contracts/{id}` and renter `GET /contracts/{id}` | gain `rent_per_period` (int TZS) beside the existing `rent_amount`, `rent_period_days` and `payment_period_days`. `rent_per_period = round(rent_amount × payment_period_days / rent_period_days)` — the schedule's proration rounding, so it equals a full schedule row's amount. |
| `GET /contracts/{id}/document` | `{{rent}}` in the rendered terms is now the **per-payment-period** amount; new template variable `{{rent_basis}}` renders the unit price with its basis (`"TZS 100,000 / 30 days"`). Contracts signed before this change keep their `terms_snapshot_html` verbatim (snapshot rule) and their `snapshot_hash` is unaffected — the hash covers the rendered terms. |
| `GET/POST/PATCH /contract-templates` | the variable list returned for the editor gains `rent_basis`; unknown variables are still a 400. |

Phase 9 notes:

- `{{rent}}` and `rent_per_period` come from one helper, `contract.RentPerPeriod(rentAmount, rentPeriodDays, paymentPeriodDays)`, shared with schedule generation, so the document and the schedules can never quote different figures.
- The **period resolver** (`internal/period`: cadence + anchor → `[from, to)` in EAT, plus bucket sizing) and the **client-IP trust rule** are internals — no endpoint exposes them. They surface only through the resolved `{from, to, cadence}` echoed by the Phase 11 report endpoints and through audit `ip` values.
- Migration `000012_part2_foundations` adds the partial unique index, `users.locale`, and the (still unused) Part 2 tables; compose init adds the `receipts` bucket.

### Phase 10 — expenses (planned)

| `GET/POST/PATCH/DELETE /org/expense-categories` | `{name(1–60), sort_order?, active?}` → `{category}` / `{items}`; seeded with the eight defaults; a category in use cannot be hard-deleted (deactivate instead). |
| `POST /expenses` | `{property_id, unit_id?, category_id, amount(int>0), incurred_on(date ≤ today+1), vendor?, reference?, note?}` → `201 {expense}`. Unit must belong to the property (400); cross-org ids → 404. |
| `PATCH /expenses/{id}` | partial → `200 {expense}`; audited before/after. A voided expense → 409 `expense_voided`. |
| `POST /expenses/{id}/void` | `{reason}` → `200 {expense}` with `status:"voided"` — append-style correction, nothing is restored or deleted. Already voided → 409 `expense_voided`. |
| `GET /expenses?property_id=&unit_id=&category_id=&from=&to=&cursor=&limit=` | → `{items:[expense], next_cursor}`; `format=csv` streams the same rows, formula-neutralised. |
| `POST /expenses/{id}/receipt` / `…/receipt/complete` / `GET …/receipt` | presigned PUT into bucket `receipts`, key `{org_id}/{expense_id}.{ext}`, ≤ 5 MiB, `image/jpeg` `image/png` `application/pdf`; read is a short-TTL presigned URL. |
| `GET /expenses/summary?cadence=&from=&to=&group_by=property\|category` | → `{from,to,cadence,previous,groups:[{id,name,total}],total}`. |

### Phase 11 — reports v2 (planned)

| `GET /reports/summary\|payment-status\|collections`, `GET /expenses/summary` | all accept `cadence=month\|quarter\|half_year\|year\|custom` with `from`/`to` (required for `custom`) and optional `anchor`; every response echoes `{from,to,cadence}` and a `previous:{from,to}` window. Range beyond 5 years → 400. |
| `GET /reports/revenue?cadence=&from=&to=&bucket=&property_id=&group_by=property` | → `{from,to,cadence,buckets:[{start,expected,collected,expenses,net}], totals, previous_totals, change_pct:{collected,expenses,net}, trend:{slope_collected_per_bucket}}`. Cash basis (non-reversed payments by `paid_at`), expected = schedules due in the bucket excluding waived, expenses by `incurred_on` excluding voided. Zero-filled; `bucket` auto `day` ≤ 62 days, `week` ≤ 26 weeks, else `month`; > 400 buckets → 400. |
| `GET /reports/occupancy?cadence=&from=&to=&bucket=&property_id=` | → `{from,to,cadence,buckets:[{start,occupied,total,rate}]}` measured at each bucket end. |
| `PUT /org/branding {dashboard_prefs}` | card ids gain `revenue`, `expenses`, `net_income`. |

### Phase 12 — theming v2 (planned)

| `GET /themes/presets` | public, cacheable → `{items:[{id,name,dark:bool,tokens:{paper,surface,ink,ink_muted,rule,primary,accent},font_id}]}` — the eight shipped presets. |
| `GET/PUT /org/branding` | `theme` becomes `{preset_id:string\|null, tokens:{…}\|null, font_id}`. The server re-validates contrast and answers **400** with `errors.theme.contrast` listing the failing pairs and their ratios when a body-text pair is below 4.5:1. Audited `org.branding_update`. |
| `GET /public/orgs/{slug}/branding`, `GET /public/units/{unit_code}` | `theme` carries the **resolved** token set (preset merged with the org's overrides; preset `ledger` when the org has none) plus `font_id`, so the renter app paints without a second call. |

### Phase 13 — language (planned)

| `POST /auth/register/renter`, `POST /orgs` | accept `locale:"sw"\|"en"` (default `sw`), carried from the public SW/EN toggle. |
| `GET /auth/me`, `GET /me/profile` | return `locale` on `user`. |
| `PATCH /me` (renter, `tms_r`) / `PATCH /org/members/me` (org user, `tms_o`) | `{locale:"sw"\|"en"}` → `200 {user}`; audited `user.locale_update`. |
| `POST /notifications/custom` | body becomes `{body_sw?, body_en?, recipients, renter_user_ids?}` — at least one language required; each recipient gets the body for their locale, falling back to the other when only one is given. `202 {batch_id, queued:{sw,en}, skipped:n}`; the log row records the language used. Insufficient credits → 409 `insufficient_sms_credits {needed, balance}`. |
| `GET /org/notification-settings` | `language` is relabelled in the UI as the default for renters without a preference; the wire field is unchanged (`orgs.settings.sms_language`). |
| `POST /contracts` | optional `language` (defaults to the renter's locale); the snapshot records it. |

### Phase 14 — SMS credits & platform templates (planned)

| `GET /org/sms-credits` (audience org) | → `{balance, low_watermark, held_count}`. |
| `GET /admin/orgs/{id}/sms` | → `{balance, low_watermark, used_30d, held_count, ledger:[{delta,balance_after,reason:"topup"\|"adjust"\|"debit"\|"refund",note,admin_name,created_at}]}`. |
| `POST /admin/orgs/{id}/sms/topup` | `{credits(int>0), note}` → `200 {balance}`; releases `held_no_credit` rows in queue order. |
| `POST /admin/orgs/{id}/sms/adjust` | `{delta(int≠0), note}` → `200 {balance}`; balance never goes below 0 (400). |
| `PATCH /admin/orgs/{id}/sms` | `{low_watermark(int≥0)}` → `200 {low_watermark}`. |
| `GET /admin/templates` | → `{items:[{kind,sw,en,variables:[…],locked,version,updated_by,updated_at}]}`. |
| `PUT /admin/templates/{kind}` | `{sw, en}` (both required) → `200 {template}`; unknown variable → 400, > 3 segments → 200 with a `warnings` array; records a version; audited. |
| `PATCH /admin/templates/{kind}` | `{locked:bool}` → `200 {template}`; `otp` ships locked. |
| `POST /admin/templates/{kind}/preview` | `{language, sample?}` → `200 {body, segments, encoding:"gsm"\|"ucs2"}`. |
| `POST /admin/templates/{kind}/revert` | `{version}` → `200 {template}` (restored as a new version). |
| `PUT /org/notification-settings` | an override of a locked kind → **409 `template_locked`**; the GET marks locked kinds read-only. |
| `GET /notifications/log` | `status` gains **`held_no_credit`** (`queued\|sending\|sent\|failed\|held_no_credit`); a held row is not `failed` and is not retryable — it leaves on the next top-up. |

Credit rules: 1 credit per 160-character GSM segment (70 for UCS-2), debited **at send time** as one conditional update; `otp` and other security kinds are exempt (configurable list); an append-only `sms_credit_ledger` row records every movement with `balance_after`.

### Phase 15 — hardening (planned)

No new endpoints. Reports v2 routes enter `make loadtest` (p95 < 300 ms on the seed org), the isolation census covers every Part 2 route, and receipt uploads and admin template edits gain rate limits.

## Part 2 — Phase 10 (shipped)

The expense ledger (SPEC §5.11, FLOWS 12). Every route below is audience **org**
(`tms_o`), owner + manager — the org's two roles: whoever may record a payment
may record an expense. Errors are the same RFC-7807 documents as Phase 5, with
the machine-readable code in `type`: `category_exists`, `category_in_use`,
`expense_voided`. Another org's id is a 404 everywhere.

`category` shape: `{id, name, is_default, sort_order, active, created_at}`
`expense` shape: `{id, property:{id,name}, unit:{id,name}|null, category:{id,name}|null, amount(int TZS), incurred_on:"YYYY-MM-DD", vendor, reference, note, receipt:{present:bool, content_type|null, size|null}, recorded_by:{user_id,name}|null, status:"recorded"|"voided", voided_at|null, void_reason|null, created_at, updated_at}`

### Categories

| `GET /org/expense-categories` | → `{items:[category]}`, ordered by `sort_order` then `name`. Includes inactive rows (`active:false`) so the settings screen can switch one back on; excludes soft-deleted ones. **Seeds lazily:** an org with no categories gets the eight defaults on this read, so orgs created before Phase 10 are not left with an empty picker. |
| `POST /org/expense-categories` | `{name(1–60), sort_order?(0–10000)}` → `201 {category}`; `is_default:false`. Omitting `sort_order` appends to the end. A duplicate name (case-insensitive, ignoring soft-deleted rows) → **409 `category_exists`**. Audited `expense_category.create`. |
| `PATCH /org/expense-categories/{id}` | `{name?, sort_order?, active?}` → `200 {category}`; duplicate name → 409 `category_exists`. Audited `expense_category.update` (before/after). |
| `DELETE /org/expense-categories/{id}` | → **204**, soft delete. If any non-deleted expense is filed under it → **409 `category_in_use`** ("deactivate it instead"). Audited `expense_category.delete`. |

The eight seeded defaults, `is_default:true`, `sort_order` 1…8 in this order:
Repairs & maintenance, Utilities, Security, Cleaning, Taxes & levies, Insurance,
Management fees, Other. They are written by `POST /orgs`, by `internal/seed`, and
lazily by the list endpoint — one list, `internal/expense.DefaultCategories`.

### Expenses

| `POST /expenses` | `{property_id, unit_id?, category_id?, amount(int 1…1,000,000,000), incurred_on(date), vendor?(≤120), reference?(≤120), note?(≤1000)}` → `201 {expense}`. `incurred_on` is `YYYY-MM-DD`, no earlier than `2000-01-01` and no later than **tomorrow** on the platform wall clock (Africa/Dar_es_Salaam). A property that is not the caller's → 404. A `unit_id` that is not a unit of that property, or a `category_id` that is not an **active** category of the org → **422** with the field named in `errors`. Audited `expense.create`. |
| `PATCH /expenses/{id}` | partial, same fields → `200 {expense}`. `unit_id`/`category_id` accept an explicit `null` to clear them (an absent member leaves them alone). References are validated against the merged post-patch row, so moving an expense to another property with its old unit attached is refused. A voided expense → **409 `expense_voided`**. Audited `expense.update` (before/after). |
| `POST /expenses/{id}/void` | `{reason(1–300)}` → `200 {expense}` with `status:"voided"`, `voided_at`, `void_reason`. Append-style correction, like `payment.reverse`: nothing is deleted, nothing is restored, and the row drops out of every total. Already voided → 409 `expense_voided`. Audited `expense.void`. |
| `GET /expenses?property_id=&unit_id=&category_id=&status=&from=&to=&cadence=&anchor=&q=&cursor=&limit=` | → `{items:[expense], next_cursor, totals:{count, amount}}`. `status` is `recorded` (default), `voided` or `all`. `totals` covers the **whole filtered set**, not the page. Sorted `incurred_on DESC, created_at DESC, id DESC`; `limit` 1–200, default 50. |
| `GET /expenses?format=csv` | the same filters, no pagination, capped at 10 000 rows. Columns: `date, property, unit, category, vendor, reference, amount, status, note, recorded_by`. Free-text cells are formula-neutralised exactly as the payment-status export is (a leading `=+-@` gets a `'`). Filename `expenses-{from}-{to}.csv` — `from` is `all` when the window is unbounded below, `to` is today's date when unbounded above. |
| `GET /expenses/{id}` | → `{expense}`; another org's id → 404. |
| `GET /expenses/summary?cadence=&anchor=&from=&to=&group_by=property\|category&property_id=` | → `{window:{from,to,cadence}, previous:{from,to,cadence}, group_by, groups:[{id,name,amount,count}], total:{amount,count}, previous_total:{amount,count}, change_pct:number\|null}`. `status:"recorded"` only. |

Phase 10 notes (implementation-confirmed):

- **Windows.** With a `cadence` (`month` default, plus `quarter`, `half_year`,
  `year`, `custom`) the window comes from the shared resolver `internal/period`,
  so "this quarter" means the same here as on every other Part 2 report: EAT
  midnight, half-open `[from, to)`, and the echoed `to` is the **exclusive** end.
  Without a cadence, `GET /expenses` reads `from`/`to` as plain **inclusive**
  dates and either may be omitted. `anchor` (a date) moves the window a
  PeriodPicker has navigated to. A custom range needs both dates and may not
  exceed five years (400 on `cadence`).
- **Summary grouping** is zero-filled from the org's own rows rather than from
  the expenses: `group_by=property` returns a group per live property and
  `group_by=category` one per **active** category, spend or no spend, so a chart
  keeps its bars and their colours from one month to the next. Rows filed under
  no category come back as an extra group with `id:null`, `name:"Uncategorised"`,
  and only when it holds something. Groups are sorted by `amount` descending,
  ties broken by name. `property_id` narrows both the groups and the totals.
- **`change_pct`** is the movement against `previous_total`, rounded to one
  decimal place, and **null when the previous window is empty** — a rise from
  nothing is a first month, not "+100%". The arithmetic is
  `internal/expense.ChangePct`, table-tested apart from the database.
- **Pagination** carries the whole sort tuple: the cursor is base64url of
  `incurred_on,created_at,id`, because a ledger sorted by a date alone would
  drop rows at every page boundary that lands inside a busy day. It is therefore
  not interchangeable with the `(timestamp,id)` cursor of the other listings.
- **`q`** matches `vendor`, `reference` and `note` with `ILIKE`, its wildcards
  escaped the way `/units` and `/renters` escape theirs (Phase 8), so a search
  for `%` finds the vendor actually named with one.
- **Receipts** live in bucket `receipts` under `{org_id}/{expense_id}.{jpg|png|pdf}`
  — both segments are ids the server holds, so no request can steer the key.
  - `POST /expenses/{id}/receipt` `{content_type:"image/jpeg"|"image/png"|"application/pdf", size(1…5 MiB)}`
    → `200 {upload_url, object_key, expires_in:900, headers:{"Content-Type":…}}`.
    The presigned PUT **must** carry that `Content-Type`: the completion callback
    checks what MinIO stored. Rate limited to **30 per hour per org**; a voided
    expense → 409 `expense_voided`; MinIO down → 503.
  - `POST /expenses/{id}/receipt/complete` `{object_key?}` → `200 {expense}`.
    The key is rebuilt from the org and the expense and only then matched against
    what was sent, so a foreign prefix is a 400 before object storage is asked
    anything; omitting it stats the three candidate keys. The object must exist,
    be ≤ 5 MiB, and carry a content type matching the extension it was issued
    under — otherwise it is deleted and the answer is 400. The accepted type and
    the real size are stored (migration `000013_expense_receipts` adds
    `expenses.receipt_content_type` and `receipt_size`) so a page of fifty ledger
    rows renders its receipt chips without fifty round trips to MinIO. Audited
    `expense.receipt_attach`.
  - `GET /expenses/{id}/receipt` → `{url, expires_in:900}`, a presigned GET; no
    receipt → 404.
  - `DELETE /expenses/{id}/receipt` → `200 {expense}`, clears the three columns
    and removes the object. This is the one deletion in the ledger, and it is
    deliberate: the receipt is an attachment, the expense row is the record.
    Audited `expense.receipt_remove`.
- **Schema:** the tables came with `000012_part2_foundations`; Phase 10 adds only
  `000013_expense_receipts` (the two receipt metadata columns). `expenses` and
  `expense_categories` join the SPEC §2.1 org-scope guard, so a query against
  either without an `org_id =` filter now fails the build.

Phase 10 audit actions: `expense_category.create`, `expense_category.update`,
`expense_category.delete`, `expense.create`, `expense.update`, `expense.void`,
`expense.receipt_attach`, `expense.receipt_remove`.

## Part 2 — Phase 11 (shipped)

Reports v2 (SPEC §5.9, FLOWS 9, PLAN2 Phase 11): the same window vocabulary on
every report, two new series, and a per-property breakdown. Audience **org**
(`tms_o`), the same roles as the Phase 7 reports. Another org's `property_id`
matches nothing rather than erroring — a foreign id is never confirmed.

### The shared window

`cadence=month|quarter|half_year|year|custom`, `anchor=YYYY-MM-DD`, `from`, `to`
are accepted by `GET /reports/summary`, `/reports/payment-status`,
`/reports/collections`, `/reports/revenue`, `/reports/occupancy` and
`GET /expenses/summary`. They resolve through `internal/period` on the Dar es
Salaam wall clock, so "this quarter" means the same thing on every one of them.

Every response carries:

- `window:{from, to, cadence}` — half-open: `from` inclusive, **`to` exclusive**.
- `previous:{from, to, cadence}` — the equivalent span immediately before (the
  previous calendar unit for a calendar cadence, an equal-length span for a
  custom range).

`anchor` names the unit for a calendar cadence (any day in May selects May) and
defaults to today; `from`/`to` are read as **inclusive** wire dates and are
required for `custom`. `cadence` defaults to `month`.

Refusals separate the malformed from the impossible:

| unknown `cadence` or `bucket`, unparseable `anchor`/date | **400** `errors.cadence` / `errors.bucket` |
| `custom` with `from` after `to`, a range beyond five years, or a missing date | **422** "window cannot be resolved", `errors.cadence` |
| a window needing more than 400 buckets at the requested size | **422** `type:"too_many_buckets"` |

`GET /reports/summary` also still takes the Phase 7 `period=month|YYYY-MM`,
translated to `cadence=month` with that month's anchor; sending both `period`
and `cadence` is a 400 on `period`. `GET /expenses` (the ledger listing, not the
summary) keeps its Phase 10 behaviour: 400 for every window refusal.

### What the four existing reports gained

| `GET /reports/summary` | `window`, `previous`, `previous_totals:{expected,collected,outstanding,overdue_count,overdue_amount}` and `change_pct` keyed by those same five names. The Phase 7 `period` block is unchanged — still **inclusive** `from`/`to` and the same five figures — so existing clients keep working. |
| `GET /reports/payment-status` | `window` and `previous`, beside the unchanged `items`. The window is context for the page, **not** a filter: a renter's standing is a fact about now. |
| `GET /reports/collections` | `window`, `previous`, `group` (the size actually used), `previous_totals:{expected,collected}` and `change_pct:{expected,collected}`. With a `cadence` the window drives the range and the grouping defaults to the auto-sized bucket (`bucket=` is accepted as a synonym for `group=`); without one, the Phase 7 defaults stand — `group=month`, the twelve months ending today, `from`/`to` inclusive — and the echoed window is `cadence:"custom"`. Buckets stay calendar-aligned here, as they always were. |
| `GET /expenses/summary` | unchanged shape (it already carried `window`, `previous`, `previous_total`, `change_pct`); only the 422 refusals above are new. |

### `GET /reports/revenue`

`?cadence=&anchor=&from=&to=&bucket=day|week|month&property_id=`

→ `{window, previous, bucket, buckets:[{start,expected,collected,expenses,net}], totals:{expected,collected,expenses,net}, previous_totals:{…}, change_pct:{expected,collected,expenses,net}, trend:{slope_collected_per_bucket}, collection_rate}`

- **collected** = non-reversed, non-deleted payments by `paid_at`, bucketed on
  the EAT wall clock. **expected** = `payment_schedules` by `due_date`,
  excluding `waived` (which is how a terminated tenancy's remaining periods drop
  out — FLOWS 6.5 waives them). **expenses** = `recorded` expenses by
  `incurred_on`. **net** = collected − expenses: cash in minus cash out, because
  a bank balance does not move on an invoice.
- `bucket` is auto-sized when absent — `day` ≤ 62 days, `week` ≤ 26 weeks, else
  `month` — and echoed. Buckets are zero-filled, and the **first bucket starts
  on the window's own first day**: a custom range opened on the 12th reports
  from the 12th rather than snapping back to the 1st.
- `trend.slope_collected_per_bucket` is the least-squares gradient of the
  collected series against its bucket index, rounded to one decimal; 0 for a
  series of fewer than two points.
- `collection_rate` is `collected / expected` as a fraction, **null** when
  nothing was expected.
- `change_pct` members are percentages rounded to one decimal and are **null**
  when the previous window's figure was zero — a rise from nothing is a first
  period, not "+100%" (Phase 10's rule, now `internal/report.ChangePct`).
- `property_id` narrows all three series alike: payments through
  contract → unit → property, schedules likewise, expenses directly.

`?group_by=property` (with the same parameters) →
`{window, previous, groups:[{id,name,expected,collected,expenses,net,collection_rate}], totals, previous_totals, change_pct}`

One row per **live property**, zero-filled from the property list rather than
from the money, so a block that earned nothing keeps its place and its colour in
the legend. Sorted by `net` descending, ties broken by name. The rows always add
up to `totals`. Any `group_by` other than `property` is a 400.

### `GET /reports/occupancy`

`?cadence=&anchor=&from=&to=&bucket=&property_id=` →
`{window, previous, bucket, buckets:[{start, units_total, units_occupied, occupancy_pct}], current:{units_total, units_occupied, occupancy_pct}}`

- Each bucket is measured on the **last day inside it**: a month's point is how
  full the portfolio was on the 31st, not on the 1st of the month after.
- A unit is occupied on day *d* when a contract covers it — `start_date ≤ d <
  end_date`, `end_date` being exclusive as everywhere else (SPEC §4). Statuses
  `active`, `expiring`, `ended` and `terminated` all count, because occupancy is
  a history: a unit let in March was let in March whatever happened since.
  `draft` and `pending_signature` never count — nobody moved in. A terminated
  tenancy counts **through its `termination_effective_date` inclusive**, the
  same day its remaining schedules stop being waived.
- `units_total` counts the org's non-deleted units that already existed on that
  day (by `created_at`), so a bucket before a unit was created does not count
  it. A tenancy on a since-deleted unit is dropped, so the ratio cannot exceed 1.
- `occupancy_pct` is a **percentage, 0–100**, rounded to one decimal (unlike the
  Phase 7 summary's `occupancy_rate`, which is a 0–1 fraction).
- `current` is the most recent real measurement the window contains: today when
  today falls inside it, otherwise the nearest end of it — a window that has not
  finished is never measured at a day that has not happened.

Phase 11 notes:

- **Indexes** (`000014_report_indexes`): `payments (org_id, paid_at) WHERE
  deleted_at IS NULL AND reversed_at IS NULL`, `expenses (org_id, incurred_on)
  WHERE status='recorded' AND deleted_at IS NULL`, and `contracts (org_id,
  start_date, end_date) WHERE deleted_at IS NULL`. The other two the phase
  called for already existed: `expenses (org_id, property_id, incurred_on)`
  (000012) and `payment_schedules (org_id, due_date, id)` (000007).
- **Bucketing is done in Go**, over day-grain SQL aggregates, rather than with
  `date_trunc`: the resolver's first bucket may start mid-month, and two
  alignments that disagree by eleven days would be a wrong chart. A five-year
  window is at most ~1830 rows per series.
- Every series endpoint runs the org-scoped overdue flip first, as the Phase 7
  reports do.

## Part 2 — Phase 12 (shipped)

Theming v2 (SPEC §2.0/§4 `org_themes`, PLAN2 Phase 12). **The backend is the
source of truth for themes**: the eight presets and the contrast validator live
in `backend/internal/theme` (`presets.json`, `go:embed`-ed), and the frontends
fetch them rather than keeping a second copy that could disagree with the copy
the server will accept.

A theme is **seven colours and a font**:

`tokens` = `{paper, surface, ink, ink_muted, rule, primary, accent}`, each a
canonical lower-case `#rrggbb` (`#0A7C4A` is accepted on input and stored and
returned as `#0a7c4a`; shorthand `#abc` and named colours are refused).
`font_id` ∈ `bricolage | archivo | instrument | hanken`.

Everything derived from those — pressed/tinted primaries, `on-primary`, faint
ink — is computed **client-side** and never stored: a derived value in the
database is a value that can disagree with the thing it derives from.

`theme` shape (returned by every branding endpoint):
`{preset_id:string|null, tokens:{7 keys}, font_id, dark:bool, source:"preset"|"custom"|"legacy"|"default", primary_color}`
— `primary_color` is `tokens.primary` under its Phase 4 name, kept (with
`font_id`) so clients written against Phase 4 keep working unchanged.

### `GET /themes/presets`

Public, rate-limited per IP like the other `/public` routes (60/min) →
`{presets:[{id, name, dark, tokens, font_id}]}`, in display order:

| id | name | dark | font | paper | surface | ink | ink_muted | rule | primary = accent |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `ledger` | Ledger | no | bricolage | `#fbfbf7` | `#ffffff` | `#1c2b5a` | `#4a5680` | `#cbd3e8` | `#2b4fd0` |
| `night_ledger` | Night ledger | **yes** | bricolage | `#14161c` | `#1c1f27` | `#e8eaf2` | `#aab1c7` | `#343a4a` | `#96b4ff` |
| `warm_paper` | Warm paper | no | instrument | `#faf5ec` | `#fffdf8` | `#33291d` | `#6b5844` | `#ddd0b8` | `#9a5423` |
| `cool_slate` | Cool slate | no | archivo | `#f3f5f7` | `#ffffff` | `#1f2933` | `#4d5a67` | `#ccd5dd` | `#2f6382` |
| `forest` | Forest | no | hanken | `#f3f7f2` | `#ffffff` | `#1b2e21` | `#45604d` | `#c9dbc9` | `#1f6640` |
| `ocean` | Ocean | no | archivo | `#f1f6fa` | `#ffffff` | `#14303f` | `#43606f` | `#c3d7e3` | `#12607f` |
| `high_contrast` | High contrast | no | archivo | `#ffffff` | `#ffffff` | `#000000` | `#1a1a1a` | `#000000` | `#0000c8` |
| `minimal_white` | Minimal white | no | hanken | `#ffffff` | `#fafafa` | `#18181b` | `#52525b` | `#dcdce0` | `#3f3f46` |

`ledger` is byte-identical to `packages/ui/src/tokens.css`, and a test pins it
there so the served default and the CSS fallback cannot drift apart.

### The contrast guard

`theme.Validate(tokens, font_id)` returns a `Failure` per broken rule —
`{pair, ratio, minimum, message}` — using WCAG 2.x relative luminance on sRGB:

| pair | minimum | why |
| --- | --- | --- |
| `ink/paper`, `ink/surface` | 4.5 | AA body text, on the page **and** on a sheet |
| `ink_muted/paper`, `ink_muted/surface` | 4.5 | AA secondary text |
| `on_primary/primary` | 4.5 | the button label; `on_primary` is `#ffffff`, or `#1c1917` when `luminance(primary) > 0.4` — the same rule the UI applies |
| `primary/paper` | 3.0 | AA non-text UI: a button must be findable |
| `rule/paper` | 1.2 | not a WCAG number — a ledger rule below it is simply not there |

Hex-format and font-id problems are reported first and **short-circuit** the
contrast pass (a ratio measured against a colour that does not parse is a
number that means nothing). Every shipped preset passes; a test asserts it.

### `GET /org/branding`, `PUT /org/branding`

`theme` on the response is the **resolved** block above. `PUT` accepts
`theme:{preset_id?, tokens?, font_id?, primary_color?}` — every member
optional:

- `preset_id` must be one of the eight (else 400 `errors["theme.preset_id"]`).
- `tokens`, if present, must be the **whole** seven-key set — a missing or an
  unknown key is a 400 on `errors["theme.tokens"]`. A partial override would
  leave the rest to whatever the app last had.
- `primary_color` alone (the Phase 4 body) still works: it recolours
  `primary`+`accent` on top of the current base and is validated like any other
  custom set.
- `font_id` outside the whitelist → 400 `errors["theme.font_id"]`. Field-level
  problems are reported **together**, not one per round trip.

A theme that fails the contrast guard is **400 `application/problem+json`**
with both `errors` (`{"theme.tokens": "3 contrast failures"}`, for the form)
and a top-level **`failures:[{pair, ratio, minimum, message}]`** array, for the
advanced panel's per-swatch badges. Nothing is written on a rejection.

On success the choice is upserted into `org_themes` (`org_id` PK: preset id,
`tokens` — `{}` for a plain preset — and font), audited **`branding.theme_update`**
with before/after, and the resolved `primary_color`/`font_id` are **mirrored
into the legacy `org_branding.theme` JSON** so every Phase 4 reader still sees a
coherent answer.

**Resolution order** (`theme.Resolve`), highest first:

1. explicit `tokens` on the `org_themes` row → `source:"custom"` (the row's
   `preset_id` survives as the label the base came from — "Night ledger, edited");
2. the row's `preset_id` → `source:"preset"`;
3. a Phase 4 `org_branding.theme.primary_color` that differs from the schema
   default `#1B4DB1` → ledger with `primary`+`accent` replaced by it and the
   legacy font, `source:"legacy"`;
4. the `ledger` preset → `source:"default"`.

`dark` is computed from `paper` (`luminance < 0.5`), so an override of a dark
preset with a white paper is correctly no longer dark.

### `GET /public/orgs/{slug}/branding`, `GET /public/units/{unit_code}`

Both return the same fully resolved `theme` object (including `primary_color`
and `font_id`), so a renter's QR landing and the landlord's own screens paint
identically from one theme — **one theme covers both apps** (DECISIONS.md) —
and the renter app paints without a second call.

Phase 12 notes:

- **Isolation.** The theme is written from the session's own org, so there is
  no cross-org write to attempt; the suite instead asserts that org A saving a
  theme leaves org B's private *and* public branding untouched. `/themes/presets`
  is in the route census as `open` — platform data, identical for every org.
- **`org_themes` has no separate `org_id` index**: `org_id` is its primary key,
  and a second index on the same column would be dead weight. The migration
  guard was widened to accept that shape.

## Part 2 — Phase 13 (shipped)

Swahili/English per user (SPEC §3.2/§5.13/§6, PLAN2 Phase 13). The rule the
whole phase turns on: **the language of a message is a fact about the person
receiving it.** `users.locale` decides; `orgs.settings.sms_language` survives
only as the default for a renter who has never expressed a preference. A
landlord who reads English no longer sends English to a renter who does not.

`locale` is a closed set of two: `"sw" | "en"`. Anything else is a **400**
validation error (`errors.locale = "must be one of: sw, en"`), like every other
`OneOf` field on the platform. New accounts default to `sw`.

### Locale on the account

| route | change |
| --- | --- |
| `POST /auth/register/renter` | accepts optional `locale` — the value of the public SW/EN toggle at registration. Absent → `sw`. Recorded on the `auth.register_renter` audit row. |
| `POST /orgs` | accepts optional `locale` for the owner being created. Recorded on `org.create`. |
| `POST /org/members` | accepts optional `locale`; the invited member's screens open in it. The `member` in the response carries `locale`. |
| `POST /auth/otp/send` | accepts optional `locale`. It is a **hint, not a preference**: it decides the language of this one code and only for a phone number that has no account yet. A number that resolves to a user is sent the code in that user's own locale. Nothing is written to `users`. |
| `GET /auth/me`, `POST /auth/login`, every `{user}` payload | `user.locale` is returned, so the apps paint without a second round trip. |
| `GET /me/profile` | `user.locale` is returned alongside the account fields. |
| `GET /org/members` | each `member` carries `locale`. |

### `PATCH /me` (renter, `tms_r`) and `PATCH /org/members/me` (org user, `tms_o`)

The language switch, one endpoint per audience. Both take exactly
`{"locale": "sw"|"en"}` and answer `200 {user}` with the full user shape:

```json
{"user":{"id":"…","kind":"renter","phone":"+255755000111","email":null,
         "full_name":"Asha Mwakalinga","email_verified":false,
         "status":"active","locale":"en","created_at":"…"}}
```

`locale` is **required** on these two routes (a PATCH that exists to set it):
omitting it is `400 {"errors":{"locale":"locale is required"}}`. Both are
audited `user.locale_update` with `before`/`after` = `{"locale":"…"}`.

Neither route names a user id — **a member can only ever move their own**.
`PATCH /org/members/me` lives under `/org/members` because it is the member's
own row, not because it can reach another's.

### Language on every renter-directed SMS

Resolution is one total function, `notify.LanguageFor(userLocale, orgLanguage)`:
the recipient's own locale, then the org's default, then `sw`. It never fails —
a user row that predates `users.locale` and an org whose settings blob was
never written both still get a language.

Every renter-directed enqueue now resolves it against the recipient rather than
the org: link approved/rejected, contract ready/terminated, welcome, thank you,
the `reminder_7d` / `reminder_due` / `overdue_daily` sweep, the unsigned-contract
nudge, and the login/sign OTP. The scheduler joins `users` for `locale`, so an
English-speaking landlord's Swahili renter still gets Swahili.

`notification_log.language` records what each message **was actually written
in**, and is returned on every `GET /notifications/log` item as `language`. The
delivery log can answer "which language did this renter get?" as data rather
than by reading the prose.

The OTP body moved out of the auth handler into the platform template
catalogue as kind `otp` with a `{{code}}` variable. It is **not** in
`TemplateKinds()` and cannot be overridden per org: nobody should be able to
re-word the message that lets a person into their own account.

### `POST /notifications/custom` — bilingual bulk send

```json
{"recipients":"all_active"|"selected", "renter_user_ids":["…"],
 "body_sw":"Habari {{name}}, maji yatakatika kesho.",
 "body_en":"Hello {{name}}, the water will be off tomorrow."}
```

At least one of `body_sw` / `body_en` is required. Each recipient gets the body
for their resolved language; **when only one body is given, everybody gets
it** — silence is not the safer failure for "the water is off tomorrow", and
the response says which language each message actually went out in.

`body` (Phase 6, single-language) still works and stands for both languages, so
an existing caller keeps working unchanged. It is ignored when either of the
new fields is present.

Each body is validated **separately**, under its own field name, so the error
names the tab the landlord typed in — `errors.body_sw`, `errors.body_en`, or
`errors.body` for the legacy field. Same rules as Phase 6: ≤ 320 characters, no
control characters, only `{{name}} {{unit}} {{property}} {{org}}`. No body at
all → `400 {"errors":{"body_sw":"provide body_sw, body_en, or both"}}`.

Response is **202**, with the Phase 6 fields plus `by_language`:

```json
{"batch_id":"e644a40e-fcd3-423b-b7e0-7c75f4573689",
 "queued":4, "skipped":0,
 "by_language":{"sw":3, "en":1}}
```

> Note: the planned contract above wrote this as `queued:{sw,en}`. It shipped
> as a scalar `queued` plus a separate `by_language` map, so the Phase 6
> `{queued, skipped}` shape is unchanged and the per-language counts are
> additive rather than a breaking re-type of an existing field.

Audited `notification.custom` with `body_sw`, `body_en` and `by_language`.

### `GET /notifications/custom/recipients-preview` (audience org)

The compose screen's question, asked before anything is sent. Takes the same
filters as the send, as query parameters:
`?recipients=all_active` or `?recipients=selected&renter_user_ids=<uuid>,<uuid>`
(comma-separated), and resolves them through the same helper, so the counts it
shows are the counts the send will produce.

```json
{"count":4, "skipped":0, "by_language":{"sw":3, "en":1}}
```

`skipped` counts renters with no phone number on file. Ids belonging to another
org are skipped, never a 404 — the request was well formed, and the count is
the honest answer. Unknown `recipients` → 400.

### Bilingual contract templates

`contract_templates` gains **`body_html_sw`**, the Swahili twin of `body_html`
(which stays the English body). Empty means "this org has no Swahili terms".

| route | change |
| --- | --- |
| `GET /contract-templates/{id}` | returns `body_html_sw` alongside `body_html` (both omitted when empty). |
| `POST /contract-templates` | accepts `body_html_sw`. Optional — `body_html` remains required. |
| `PATCH /contract-templates/{id}` | accepts `body_html_sw`; an explicit `""` **clears** it (an org that decides it does not want one), rather than being a validation failure. |
| `POST /contract-templates/{id}/preview` | accepts `{language?:"sw"\|"en"}` and returns `language` — the body actually previewed. Asking for Swahili from a template that has none previews the English one rather than a blank page. `{{due_day}}` renders in the previewed language. |

Both bodies go through the same sanitizer on write **and** on render, so a body
stored before a policy change can never escape the current allowlist. Both
carry exactly the same `{{variables}}`; a test pins that they cannot drift.

`contract.DefaultTemplateBodySW` is the Swahili default, in the register a
Tanzanian tenancy agreement is actually written in ("Mkataba wa Upangaji",
"Mwenye Nyumba", "Mpangaji", "Kodi"). It is seeded on org bootstrap and by the
seeder; migration 000015 seeds the byte-identical body for the orgs that
already existed — but **only where the English body is still the platform
wording verbatim**. A body the landlord has since edited is left alone: its
Swahili counterpart is theirs to write, and guessing at one would put words the
landlord never approved into a contract.

### `POST /contracts {language?}`

Optional `language`. Absent means **the renter's own locale** — the document a
person signs should be in the language they read. The landlord may override it
for one contract.

The Swahili body is used when the org has written one; an org that has not
keeps issuing the English document rather than a blank one, and `language`
records which of the two the renter actually received. `{{due_day}}` and
`{{rent_basis}}` render in the contract's language, so a Swahili document has
no English clause in the middle of it.

`contracts.language` is stored beside the terms it snapshots and returned on
every `contract` DTO. **Contracts issued before Part 2 read `"en"`** — the sole
template body was the English one, which is what they were rendered from.

The snapshot flow is untouched: `snapshot_hash` still covers the rendered HTML,
so a contract issued in Swahili verifies exactly as an English one does.

### Migration `000015_language`

Adds `notification_log.language` (`NOT NULL DEFAULT 'sw'`, checked `IN
('sw','en')`), `contract_templates.body_html_sw` (`NOT NULL DEFAULT ''`, seeded
as described above) and `contracts.language` (`NOT NULL DEFAULT 'en'`, checked
`IN ('sw','en')`). `users.locale` already existed from 000012.

### Audit actions

`user.locale_update` joins the Part 2 action list.
