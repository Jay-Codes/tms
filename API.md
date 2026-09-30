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

## Part 2 — shipped contract (Phases 9–14)

Everything in this section is **live**. It replaces the per-phase "planned" and
"shipped" sections the phases were written against: where the plan and the code
disagreed, the code is what is written below and the difference is called out in
[Deviations from the planned contract](#deviations-from-the-planned-contract) at
the end. Plan and phase order in [PLAN2.md](PLAN2.md); semantics in SPEC §2.0,
§3.2, §5.11–5.13, §6, §7; flows 9, 12 and 13 in [FLOWS.md](FLOWS.md).

Part 2 keeps every Phase 1–7 convention: audience cookies, RFC-7807 problems
with the machine-readable code in `type`, **400** for a malformed field with
`errors` populated, **404** for an id outside the caller's scope (never 403),
and an audit row on every mutation. Two refusals are new and used only by the
reporting windows: **422** for a request that parses but cannot be satisfied
(`window cannot be resolved`, `too_many_buckets`), and **409** for the two Part 2
business conflicts (`insufficient_sms_credits`, `template_locked`).

### Payment periods — the single recommended badge

Exactly one payment period per org carries the badge, enforced by a partial
unique index (`payment_periods (org_id) WHERE is_recommended AND deleted_at IS
NULL`). Audience org (`tms_o`), owner + manager.

| `POST /org/payment-periods/{id}/recommend` | (no body) → `200 {period}` — moves the badge to this period and clears it from every other one in the org, in one transaction. An inactive or soft-deleted period → **409 `period_inactive`**; another org's id → 404. Audited `payment_period.recommend`, `before`/`after` naming the period that lost the badge and the one that gained it. |
| `GET /org/payment-periods` | unchanged shape, with a new guarantee: **at most one item has `is_recommended: true`**. Ordering stays recommended first, then `sort_order`. |
| `PATCH /org/payment-periods/{id}` | rejects `is_recommended` — the badge moves only through `/recommend`. |
| `POST /org/payment-periods/restore-recommended` | restores any missing seeded presets (30/90/180/365) **unbadged**; if the org has no recommended period at all, Monthly is badged, so there is never zero. |
| `GET /public/units/{unit_code}` | unchanged shape; `periods[]` is ordered recommended-first and the badge now marks exactly one entry. |

Org bootstrap (`POST /orgs`) and `internal/seed` badge Monthly and nothing else.

### Contracts — rent per payment period, and the document's language

| `GET /contracts/{id}` (org **and** renter audience) | gains `rent_per_period` (int TZS) beside `rent_amount`, `rent_period_days` and `payment_period`, and `language` (`"sw"\|"en"`). `rent_per_period = round(rent_amount × payment_period_days / rent_period_days)` — the schedule's own proration rounding, so it equals a full schedule row's amount. It is **derived, never stored**: the snapshot columns stay the source of truth. |
| `GET /contracts/{id}/document` | `{{rent}}` in the rendered terms is the **per-payment-period** amount; `{{rent_basis}}` renders the unit price with its basis (`"TZS 100,000 / 30 days"`). Contracts signed before Phase 9 keep `terms_snapshot_html` verbatim (snapshot rule) and their `snapshot_hash` is unaffected — the hash covers the rendered terms. |
| `POST /contracts` | optional `language` (`"sw"\|"en"`), defaulting to **the renter's own locale**. The Swahili body is used when the org has written one; an org that has not keeps issuing the English document rather than a blank one, and `language` records which of the two the renter actually received. `{{due_day}}` and `{{rent_basis}}` render in the contract's language. An unknown value → 400. |
| `GET/POST/PATCH /contract-templates` | the variable list gains `rent_basis`; unknown variables are still a 400. `body_html_sw` is the Swahili twin of `body_html` (below). |

`{{rent}}` and `rent_per_period` come from one helper,
`contract.RentPerPeriod(rentAmount, rentPeriodDays, paymentPeriodDays)`, shared
with schedule generation, so the document and the schedules can never quote
different figures. `contracts.language` is stored beside the terms it snapshots
and returned on every `contract` DTO; **contracts issued before Part 2 read
`"en"`** — the sole template body was the English one.

### Expenses

The expense ledger (SPEC §5.11, FLOWS 12). Audience **org** (`tms_o`), owner +
manager — whoever may record a payment may record an expense. Codes in `type`:
`category_exists`, `category_in_use`, `expense_voided`. Another org's id is a 404
everywhere.

`category` shape: `{id, name, is_default, sort_order, active, created_at}`
`expense` shape: `{id, property:{id,name}, unit:{id,name}|null, category:{id,name}|null, amount(int TZS), incurred_on:"YYYY-MM-DD", vendor, reference, note, receipt:{present:bool, content_type|null, size|null}, recorded_by:{user_id,name}|null, status:"recorded"|"voided", voided_at|null, void_reason|null, created_at, updated_at}`

#### Categories

| `GET /org/expense-categories` | → `{items:[category]}`, ordered by `sort_order` then `name`. Includes inactive rows (`active:false`) so the settings screen can switch one back on; excludes soft-deleted ones. **Seeds lazily:** an org with no categories gets the eight defaults on this read, so orgs created before Phase 10 are not left with an empty picker. |
| `POST /org/expense-categories` | `{name(1–60), sort_order?(0–10000)}` → `201 {category}`; `is_default:false`. Omitting `sort_order` appends. A duplicate name (case-insensitive, ignoring soft-deleted rows) → **409 `category_exists`**. Audited `expense_category.create`. |
| `PATCH /org/expense-categories/{id}` | `{name?, sort_order?, active?}` → `200 {category}`; duplicate name → 409 `category_exists`. Audited `expense_category.update`. |
| `DELETE /org/expense-categories/{id}` | → **204**, soft delete. Any non-deleted expense filed under it → **409 `category_in_use`** ("deactivate it instead"). Audited `expense_category.delete`. |

The eight seeded defaults, `is_default:true`, `sort_order` 1…8 in this order:
Repairs & maintenance, Utilities, Security, Cleaning, Taxes & levies, Insurance,
Management fees, Other. They are written by `POST /orgs`, by `internal/seed`, and
lazily by the list endpoint — one list, `internal/expense.DefaultCategories`.

#### The ledger

| `POST /expenses` | `{property_id, unit_id?, category_id?, amount(int 1…1,000,000,000), incurred_on(date), vendor?(≤120), reference?(≤120), note?(≤1000)}` → `201 {expense}`. `incurred_on` is `YYYY-MM-DD`, no earlier than `2000-01-01` and no later than **tomorrow** on the platform wall clock (Africa/Dar_es_Salaam). A property that is not the caller's → 404. A `unit_id` that is not a unit of that property, or a `category_id` that is not an **active** category of the org → **422** with the field named in `errors`. Audited `expense.create`. |
| `PATCH /expenses/{id}` | partial, same fields → `200 {expense}`. `unit_id`/`category_id` accept an explicit `null` to clear them (an absent member leaves them alone). References are validated against the merged post-patch row, so moving an expense to another property with its old unit attached is refused. A voided expense → **409 `expense_voided`**. Audited `expense.update` (before/after). |
| `POST /expenses/{id}/void` | `{reason(1–300)}` → `200 {expense}` with `status:"voided"`, `voided_at`, `void_reason`. Append-style correction, like `payment.reverse`: nothing is deleted, nothing is restored, and the row drops out of every total. Already voided → 409 `expense_voided`. Audited `expense.void`. |
| `GET /expenses?property_id=&unit_id=&category_id=&status=&from=&to=&cadence=&anchor=&q=&cursor=&limit=` | → `{items:[expense], next_cursor, totals:{count, amount}}`. `status` is `recorded` (default), `voided` or `all`. `totals` covers the **whole filtered set**, not the page. Sorted `incurred_on DESC, created_at DESC, id DESC`; `limit` 1–200, default 50. |
| `GET /expenses?format=csv` | the same filters, no pagination, capped at 10 000 rows. Columns: `date, property, unit, category, vendor, reference, amount, status, note, recorded_by`. Free-text cells are formula-neutralised exactly as the payment-status export is (a leading `=+-@` gets a `'`). Filename `expenses-{from}-{to}.csv` — `from` is `all` when the window is unbounded below, `to` is today's date when unbounded above. |
| `GET /expenses/{id}` | → `{expense}`; another org's id → 404. |
| `GET /expenses/summary?cadence=&anchor=&from=&to=&group_by=property\|category&property_id=` | → `{window:{from,to,cadence}, previous:{from,to,cadence}, group_by, groups:[{id,name,amount,count}], total:{amount,count}, previous_total:{amount,count}, change_pct:number\|null}`. `status:"recorded"` only. |

- **Windows.** With a `cadence` the window comes from the shared resolver
  (below), so "this quarter" means the same here as on every other Part 2
  report. **Without** a cadence, `GET /expenses` reads `from`/`to` as plain
  **inclusive** dates and either may be omitted. The ledger listing keeps its
  Phase 10 behaviour of answering **400** for every window refusal, where the
  reports answer 422 for an unsatisfiable one.
- **Summary grouping** is zero-filled from the org's own rows rather than from
  the expenses: `group_by=property` returns a group per live property and
  `group_by=category` one per **active** category, spend or no spend, so a chart
  keeps its bars and their colours from one month to the next. Rows filed under
  no category come back as an extra group with `id:null`, `name:"Uncategorised"`,
  and only when it holds something. Groups are sorted by `amount` descending,
  ties broken by name. `property_id` narrows both the groups and the totals.
- **`change_pct`** is the movement against `previous_total`, rounded to one
  decimal, and **null when the previous window is empty** — a rise from nothing
  is a first month, not "+100%" (`internal/expense.ChangePct`).
- **Pagination** carries the whole sort tuple: the cursor is base64url of
  `incurred_on,created_at,id`, because a ledger sorted by a date alone would drop
  rows at every page boundary inside a busy day. It is therefore not
  interchangeable with the `(timestamp,id)` cursor of the other listings.
- **`q`** matches `vendor`, `reference` and `note` with `ILIKE`, its wildcards
  escaped the way `/units` and `/renters` escape theirs (Phase 8).

#### Receipts

Bucket `receipts`, key `{org_id}/{expense_id}.{jpg|png|pdf}` — both segments are
ids the server holds, so no request can steer the key.

| `POST /expenses/{id}/receipt` | `{content_type:"image/jpeg"\|"image/png"\|"application/pdf", size(1…5 MiB)}` → `200 {upload_url, object_key, expires_in:900, headers:{"Content-Type":…}}`. The presigned PUT **must** carry that `Content-Type`: the completion callback checks what MinIO stored. Rate limited to **30 per hour per org**; a voided expense → 409 `expense_voided`; MinIO down → 503. |
| `POST /expenses/{id}/receipt/complete` | `{object_key?}` → `200 {expense}`. The key is rebuilt from the org and the expense and only then matched against what was sent, so a foreign prefix is a 400 before object storage is asked anything; omitting it stats the three candidate keys. The object must exist, be ≤ 5 MiB, and carry a content type matching the extension it was issued under — otherwise it is deleted and the answer is 400. The accepted type and the real size are stored (`expenses.receipt_content_type`, `receipt_size`) so a page of fifty ledger rows renders its receipt chips without fifty round trips to MinIO. Audited `expense.receipt_attach`. |
| `GET /expenses/{id}/receipt` | → `{url, expires_in:900}`, a presigned GET; no receipt → 404. |
| `DELETE /expenses/{id}/receipt` | → `200 {expense}`, clears the three columns and removes the object. This is the one deletion in the ledger, and it is deliberate: the receipt is an attachment, the expense row is the record. Audited `expense.receipt_remove`. |

### Reports v2 — the shared window

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
translated to `cadence=month` with that month's anchor; sending both `period` and
`cadence` is a 400 on `period`.

Audience **org** (`tms_o`), the same roles as the Phase 7 reports. Another org's
`property_id` matches nothing rather than erroring — a foreign id is never
confirmed. Every series endpoint runs the org-scoped overdue flip first, as the
Phase 7 reports do.

#### What the four existing reports gained

| `GET /reports/summary` | `window`, `previous`, `previous_totals:{expected,collected,outstanding,overdue_count,overdue_amount}` and `change_pct` keyed by those same five names. The Phase 7 `period` block is unchanged — still **inclusive** `from`/`to` and the same five figures — so existing clients keep working. Its `occupancy_rate` remains a **0–1 fraction**. |
| `GET /reports/payment-status` | `window` and `previous`, beside the unchanged `items`. The window is context for the page, **not** a filter: a renter's standing is a fact about now. |
| `GET /reports/collections` | `window`, `previous`, `group` (the size actually used), `previous_totals:{expected,collected}` and `change_pct:{expected,collected}`. With a `cadence` the window drives the range and the grouping defaults to the auto-sized bucket (`bucket=` is accepted as a synonym for `group=`); without one, the Phase 7 defaults stand — `group=month`, the twelve months ending today, `from`/`to` inclusive — and the echoed window is `cadence:"custom"`. Buckets stay calendar-aligned here, as they always were. |
| `GET /expenses/summary` | the shape above; only the 422 refusals are new. |

#### `GET /reports/revenue`

`?cadence=&anchor=&from=&to=&bucket=day|week|month&property_id=`

→ `{window, previous, bucket, buckets:[{start,expected,collected,expenses,net}], totals:{expected,collected,expenses,net}, previous_totals:{…}, change_pct:{expected,collected,expenses,net}, trend:{slope_collected_per_bucket}, collection_rate}`

- **collected** = non-reversed, non-deleted payments by `paid_at`, bucketed on
  the EAT wall clock. **expected** = `payment_schedules` by `due_date`, excluding
  `waived` (which is how a terminated tenancy's remaining periods drop out —
  FLOWS 6.5 waives them). **expenses** = `recorded` expenses by `incurred_on`.
  **net** = collected − expenses: cash in minus cash out, because a bank balance
  does not move on an invoice.
- `bucket` is auto-sized when absent — `day` ≤ 62 days, `week` ≤ 26 weeks, else
  `month` — and echoed. Buckets are zero-filled, and the **first bucket starts on
  the window's own first day**: a custom range opened on the 12th reports from
  the 12th rather than snapping back to the 1st.
- `trend.slope_collected_per_bucket` is the least-squares gradient of the
  collected series against its bucket index, rounded to one decimal; 0 for a
  series of fewer than two points.
- `collection_rate` is `collected / expected` as a **fraction (0–1)**, null when
  nothing was expected.
- `change_pct` members are percentages rounded to one decimal and are **null**
  when the previous window's figure was zero.
- `property_id` narrows all three series alike: payments through contract → unit
  → property, schedules likewise, expenses directly.

`?group_by=property` (same parameters) →
`{window, previous, groups:[{id,name,expected,collected,expenses,net,collection_rate}], totals, previous_totals, change_pct}`

One row per **live property**, zero-filled from the property list rather than
from the money, so a block that earned nothing keeps its place and its colour in
the legend. Sorted by `net` descending, ties broken by name. The rows always add
up to `totals`. Any `group_by` other than `property` is a 400.

#### `GET /reports/occupancy`

`?cadence=&anchor=&from=&to=&bucket=&property_id=` →
`{window, previous, bucket, buckets:[{start, units_total, units_occupied, occupancy_pct}], current:{units_total, units_occupied, occupancy_pct}}`

- Each bucket is measured on the **last day inside it**: a month's point is how
  full the portfolio was on the 31st, not on the 1st of the month after.
- A unit is occupied on day *d* when a contract covers it — `start_date ≤ d <
  end_date`, `end_date` being exclusive as everywhere else (SPEC §4). Statuses
  `active`, `expiring`, `ended` and `terminated` all count, because occupancy is
  a history: a unit let in March was let in March whatever happened since.
  `draft` and `pending_signature` never count. A terminated tenancy counts
  **through its `termination_effective_date` inclusive**, the same day its
  remaining schedules stop being waived.
- `units_total` counts the org's non-deleted units that already existed on that
  day (by `created_at`). A tenancy on a since-deleted unit is dropped, so the
  ratio cannot exceed 1.
- `occupancy_pct` is a **percentage, 0–100**, rounded to one decimal — unlike
  `collection_rate` and the Phase 7 summary's `occupancy_rate`, which are 0–1
  fractions.
- `current` is the most recent real measurement the window contains: today when
  today falls inside it, otherwise the nearest end of it.

#### Dashboard cards

`PUT /org/branding {dashboard_prefs}` accepts the card ids `revenue`,
`expenses` and `net_income` alongside the Phase 7 set. Unknown card ids and
unknown keys are still a 400.

#### Notes

- **Indexes** (`000014_report_indexes`): `payments (org_id, paid_at) WHERE
  deleted_at IS NULL AND reversed_at IS NULL`, `expenses (org_id, incurred_on)
  WHERE status='recorded' AND deleted_at IS NULL`, and `contracts (org_id,
  start_date, end_date) WHERE deleted_at IS NULL`. `expenses (org_id,
  property_id, incurred_on)` (000012) and `payment_schedules (org_id, due_date,
  id)` (000007) already existed.
- **Bucketing is done in Go**, over day-grain SQL aggregates, rather than with
  `date_trunc`: the resolver's first bucket may start mid-month, and two
  alignments that disagree by eleven days would be a wrong chart.

### Themes

**The backend is the source of truth for themes**: the eight presets and the
contrast validator live in `backend/internal/theme` (`presets.json`,
`go:embed`-ed). `packages/ui` keeps a generated offline copy so the frontends can
paint before the API answers, and `TestPresetsMatchUICopy` fails the build if the
two drift.

A theme is **seven colours and a font**: `tokens` = `{paper, surface, ink,
ink_muted, rule, primary, accent}`, each a canonical lower-case `#rrggbb`
(`#0A7C4A` is accepted on input and stored and returned as `#0a7c4a`; shorthand
`#abc` and named colours are refused). `font_id` ∈ `bricolage | archivo |
instrument | hanken`.

Everything derived from those — pressed/tinted primaries, `on-primary`, faint ink
— is computed **client-side** and never stored. Stamp inks are not themable, but
they get a fixed dark step (`#4ec07f` paid, `#ff8a80` overdue) under
`data-theme="dark"`, as the chart palette does.

`theme` shape (returned by every branding endpoint):
`{preset_id:string|null, tokens:{7 keys}, font_id, dark:bool, source:"preset"|"custom"|"legacy"|"default", primary_color}`
— `primary_color` is `tokens.primary` under its Phase 4 name, kept (with
`font_id`) so clients written against Phase 4 keep working unchanged.

#### `GET /themes/presets`

Public, rate-limited per IP like the other public routes (60/min) →
**`{presets:[{id, name, dark, tokens, font_id}]}`**, in display order:

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

#### The contrast guard

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
contrast pass. Every shipped preset passes; a test asserts it.

#### `GET /org/branding`, `PUT /org/branding`

`theme` on the response is the **resolved** block above. `PUT` accepts
`theme:{preset_id?, tokens?, font_id?, primary_color?}` — every member optional:

- `preset_id` must be one of the eight (else 400 `errors["theme.preset_id"]`).
- `tokens`, if present, must be the **whole** seven-key set — a missing or an
  unknown key is a 400 on `errors["theme.tokens"]`. A partial override would
  leave the rest to whatever the app last had.
- `primary_color` alone (the Phase 4 body) still works: it recolours
  `primary`+`accent` on top of the current base and is validated like any other
  custom set.
- `font_id` outside the whitelist → 400 `errors["theme.font_id"]`. Field-level
  problems are reported **together**, not one per round trip.

A theme that fails the contrast guard is **400 `application/problem+json`** with
both `errors` (`{"theme.tokens": "3 contrast failures"}`, for the form) and a
top-level **`failures:[{pair, ratio, minimum, message}]`** array, for the
advanced panel's per-swatch badges. Nothing is written on a rejection.

On success the choice is upserted into `org_themes` (`org_id` PK: preset id,
`tokens` — `{}` for a plain preset — and font), audited **`branding.theme_update`**
with before/after, and the resolved `primary_color`/`font_id` are **mirrored into
the legacy `org_branding.theme` JSON** so every Phase 4 reader still sees a
coherent answer.

**Resolution order** (`theme.Resolve`), highest first:

1. explicit `tokens` on the `org_themes` row → `source:"custom"` (the row's
   `preset_id` survives as the label the base came from — "Night ledger, edited");
2. the row's `preset_id` → `source:"preset"`;
3. a Phase 4 `org_branding.theme.primary_color` that differs from the schema
   default `#1b4db1` → ledger with `primary`+`accent` replaced by it and the
   legacy font, `source:"legacy"`;
4. the `ledger` preset → `source:"default"`.

`dark` is computed from `paper` (`luminance < 0.5`), so an override of a dark
preset with a white paper is correctly no longer dark.

#### `GET /public/orgs/{slug}/branding`, `GET /public/units/{unit_code}`

Both return the same fully resolved `theme` object (including `primary_color` and
`font_id`), so a renter's QR landing and the landlord's own screens paint
identically from one theme — **one theme covers both apps** (DECISIONS.md) — and
the renter app paints without a second call. The admin app is never themed.

`org_themes` has **no separate `org_id` index**: `org_id` is its primary key, and
a second index on the same column would be dead weight. The migration guard was
widened to accept that shape.

### Locale, and the language of every message

**The language of a message is a fact about the person receiving it.**
`users.locale` decides; `orgs.settings.sms_language` survives only as the default
for a renter who has never expressed a preference.

`locale` is a closed set of two: `"sw" | "en"`. Anything else is a **400**
validation error (`errors.locale = "must be one of: sw, en"`), like every other
`OneOf` field on the platform. New accounts default to `sw`.

| route | change |
| --- | --- |
| `POST /auth/register/renter` | accepts optional `locale` — the value of the public SW/EN toggle at registration. Absent → `sw`. Recorded on the `auth.register_renter` audit row. |
| `POST /orgs` | accepts optional `locale` for the owner being created. Recorded on `org.create`. |
| `POST /org/members` | accepts optional `locale`; the invited member's screens open in it. The `member` in the response carries `locale`. |
| `POST /auth/otp/send` | accepts optional `locale`. It is a **hint, not a preference**: it decides the language of this one code and only for a phone number that has no account yet. A number that resolves to a user is sent the code in that user's own locale. Nothing is written to `users`. |
| `GET /auth/me`, `POST /auth/login`, every `{user}` payload | `user.locale` is returned, so the apps paint without a second round trip. |
| `GET /me/profile` | `user.locale` alongside the account fields. |
| `GET /org/members` | each `member` carries `locale`. |

#### `PATCH /me` (renter, `tms_r`) and `PATCH /org/members/me` (org user, `tms_o`)

The language switch, one endpoint per audience. Both take exactly
`{"locale": "sw"|"en"}` and answer `200 {user}` with the full user shape:

```json
{"user":{"id":"…","kind":"renter","phone":"+255755000111","email":null,
         "full_name":"Asha Mwakalinga","email_verified":false,
         "status":"active","locale":"en","created_at":"…"}}
```

`locale` is **required** on these two routes (a PATCH that exists to set it):
omitting it is `400 {"errors":{"locale":"locale is required"}}`. Both are audited
`user.locale_update` with `before`/`after` = `{"locale":"…"}`.

Neither route names a user id — **a member can only ever move their own**.
`PATCH /org/members/me` lives under `/org/members` because it is the member's own
row, not because it can reach another's.

#### Language on every renter-directed SMS

Resolution is one total function, `notify.LanguageFor(userLocale, orgLanguage)`:
the recipient's own locale, then the org's default, then `sw`. It never fails.

Every renter-directed enqueue resolves it against the recipient rather than the
org: link approved/rejected, contract ready/terminated, welcome, thank you, the
`reminder_7d` / `reminder_due` / `overdue_daily` sweep, the unsigned-contract
nudge, and the login/sign OTP. The scheduler joins `users` for `locale`, so an
English-speaking landlord's Swahili renter still gets Swahili.

`notification_log.language` records what each message **was actually written
in**, and is returned on every `GET /notifications/log` item as `language`.

The OTP body moved out of the auth handler into the platform template catalogue
as kind `otp` with a `{{code}}` variable. It is **not** in `TemplateKinds()` and
cannot be overridden per org.

#### `POST /notifications/custom` — bilingual bulk send

```json
{"recipients":"all_active"|"selected", "renter_user_ids":["…"],
 "body_sw":"Habari {{name}}, maji yatakatika kesho.",
 "body_en":"Hello {{name}}, the water will be off tomorrow."}
```

At least one of `body_sw` / `body_en` is required. Each recipient gets the body
for their resolved language; **when only one body is given, everybody gets it** —
silence is not the safer failure for "the water is off tomorrow", and the
response says which language each message actually went out in.

`body` (Phase 6, single-language) still works and stands for both languages, so
an existing caller keeps working unchanged. It is ignored when either of the new
fields is present.

Each body is validated **separately**, under its own field name, so the error
names the tab the landlord typed in — `errors.body_sw`, `errors.body_en`, or
`errors.body` for the legacy field. Same rules as Phase 6: ≤ 320 characters, no
control characters, only `{{name}} {{unit}} {{property}} {{org}}`. No body at all
→ `400 {"errors":{"body_sw":"provide body_sw, body_en, or both"}}`.

Response is **202**, with the Phase 6 fields plus `by_language` — `queued` stays
a **scalar**:

```json
{"batch_id":"e644a40e-fcd3-423b-b7e0-7c75f4573689",
 "queued":4, "skipped":0,
 "by_language":{"sw":3, "en":1}}
```

Audited `notification.custom` with `body_sw`, `body_en` and `by_language`.
Insufficient credit → **409 `insufficient_sms_credits`** (below); nothing is
queued.

#### `GET /notifications/custom/recipients-preview` (audience org)

The compose screen's question, asked before anything is sent. Same filters as the
send, as query parameters: `?recipients=all_active` or
`?recipients=selected&renter_user_ids=<uuid>,<uuid>` (comma-separated), resolved
through the same helper, so the counts it shows are the counts the send will
produce.

```json
{"count":4, "skipped":0, "by_language":{"sw":3, "en":1}}
```

`skipped` counts renters with no phone number on file. Ids belonging to another
org are skipped, never a 404. Unknown `recipients` → 400.

#### Bilingual contract templates

`contract_templates` gains **`body_html_sw`**, the Swahili twin of `body_html`
(which stays the English body). Empty means "this org has no Swahili terms".

| route | change |
| --- | --- |
| `GET /contract-templates/{id}` | returns `body_html_sw` alongside `body_html` (both omitted when empty). |
| `POST /contract-templates` | accepts `body_html_sw`. Optional — `body_html` remains required. |
| `PATCH /contract-templates/{id}` | accepts `body_html_sw`; an explicit `""` **clears** it, rather than being a validation failure. |
| `POST /contract-templates/{id}/preview` | accepts `{language?:"sw"\|"en"}` and returns `language` — the body actually previewed. Asking for Swahili from a template that has none previews the English one rather than a blank page. `{{due_day}}` renders in the previewed language. |

Both bodies go through the same sanitizer on write **and** on render, and carry
exactly the same `{{variables}}`; a test pins that they cannot drift.

`contract.DefaultTemplateBodySW` is the Swahili default, in the register a
Tanzanian tenancy agreement is actually written in ("Mkataba wa Upangaji",
"Mwenye Nyumba", "Mpangaji", "Kodi"). It is seeded on org bootstrap and by the
seeder; migration 000015 seeds it for orgs that already existed — but **only
where the English body is still the platform wording verbatim**. A body the
landlord has since edited is left alone.

### SMS credits

Prepaid credit per organisation. The movements are platform-admin (`tms_a`); the
landlord gets one read-only view of their own balance.

#### The credit unit

One credit per **SMS segment**, not per message: 160 characters in the GSM 03.38
alphabet, 70 in UCS-2, dropping to 153 / 67 once a body is long enough to be
concatenated. `notify.Segments(body)` is the single definition, and both the bulk
pre-check and the worker's debit call it, so the shortfall a landlord is quoted
is the amount that is actually taken.

Swahili is written in the Latin alphabet with no diacritics, so a Swahili
reminder costs exactly what its English twin does. An emoji or a diacritic
outside the alphabet pushes the *whole* message to UCS-2 and roughly doubles its
price; the admin template editor reports `segments` and `encoding` on every save
and preview.

Credits have **no expiry and no monthly reset**.

#### When the debit happens

At **send time**, inside the worker's claim transaction: the row moves `queued` →
`sending` and the balance drops by the message's segment count together, or
neither happens. The debit is one conditional statement —

```sql
UPDATE org_sms_credits SET balance = balance - $n
WHERE org_id = $1 AND balance >= $n RETURNING balance
```

— which is the whole of the concurrency story. Three workers racing the last two
credits serialise on the row lock and exactly one comes back with a row.

Two consequences are deliberate: a queued message that is never sent (an org
suspended before its backlog drains) **costs nothing**; and a send the provider
then rejects **has** consumed the credit — refunding one is an `adjust` an admin
makes, not something the worker guesses at.

`org_sms_credits` is created lazily — balance 0, `low_watermark` 50 — on the
first read or the first send.

**Exempt kinds** send without a debit: `otp` by default, overridable with
`SMS_CREDIT_EXEMPT_KINDS` (comma-separated; the literal `none` charges for
everything).

**Insufficient balance** → the row's status becomes **`held_no_credit`**. It is
not `failed`: nothing went wrong with the message, the retry loop leaves it
alone, `POST /notifications/log/{id}/retry` answers **409 `not_failed`**, and it
goes out unchanged on the next top-up.

Every movement writes an append-only `sms_credit_ledger` row carrying `delta`,
`balance_after`, `reason` and — for a debit — the `notification_id` it paid for.
`UPDATE` and `DELETE` on the ledger raise (trigger, migration 000012).

#### Admin (`tms_a`)

| `GET /admin/orgs/{id}/sms` | → `{balance, low_watermark, used_30d, held_count, ledger:[{delta, balance_after, reason:"topup"\|"adjust"\|"debit"\|"refund", notification_id\|null, note, admin_name, created_at}]}` — the newest **100** movements. An org id that does not exist is a 404 and creates no credit row. |
| `POST /admin/orgs/{id}/sms/topup` | `{credits(1–1000000), note(≤500)}` → **`200 {balance, released}`** (`released` = how many held rows were re-queued). Writes a `topup` ledger row, then releases the org's `held_no_credit` rows **oldest first**. Audited `sms_credits.topup`. |
| `POST /admin/orgs/{id}/sms/adjust` | `{delta(≠0, ±1000000), note}` → **`200 {balance, released}`**. A delta that would take the balance below zero is a **400** naming the current balance — a prepaid balance has no overdraft. A positive adjustment releases held rows like a top-up. Audited `sms_credits.adjust`. |
| `PATCH /admin/orgs/{id}/sms` | `{low_watermark(0–1000000)}` → `200 {low_watermark}`. Audited `sms_credits.watermark_update`. |

**Release policy:** a top-up releases **every** held row, not only the ones the
new balance covers. The debit happens at send time, so releasing more than the
org can pay for costs nothing — the worker holds the surplus again, in the same
order, on the next attempt.

All three movements carry the **target org** as `org_id` and the admin as actor,
exactly as `org.suspend` does, so the landlord reads "credits added by platform"
in their own audit page.

#### Landlord (`tms_o`)

| `GET /org/sms-credits` | → `{balance, low_watermark, held_count, low:bool}`. Read-only: credits are sold by the platform. `low` is the banner's condition (`balance < low_watermark`), computed once server-side so the three apps cannot disagree at the boundary. |
| `POST /notifications/custom` | Pre-checks credit **before** queuing: `needed` is the sum of `Segments(body)` over every recipient's *rendered* body — `{{name}}` expands differently per renter and Swahili runs longer than English — and a shortfall is **409 `insufficient_sms_credits`** carrying `{needed, balance}` as top-level members of the problem document. Nothing is queued. The check is advisory, not a reservation: two broadcasts racing one balance can both pass it and the second one's tail is held. |
| `GET /notifications/log?status=` | accepts **`held_no_credit`** alongside `queued\|sending\|sent\|failed`. |

When a debit takes a balance from at-or-above the watermark to below it, the
org's owner is emailed **once** — the crossing test is what stops an org running
at zero from mailing its owner forty times a day. In dev that is the log email
provider (`.dev/api.log`).

### Platform message templates

`platform_templates` is the source of truth for the platform's wording, seeded by
migration `000016_platform_templates_seed` from the Go catalogue in
`internal/notify/templates.go` and re-seeded (`ON CONFLICT DO NOTHING`) on every
API startup, so a kind added in a later phase reaches the table without another
migration.

`notify.Render` resolution is **org override → `platform_templates` row → the
built-in Go default**. The Go map stays as the last fallback rather than being
deleted: it is what a fresh database is seeded from, and it is what renders a
message when Postgres is unreachable at the moment a send goes out.

Resolved wording is cached in Redis under `tmpl:{kind}` for **5 minutes** and in
process for the same window; both are invalidated on every save, lock and revert.

`thank_you_settled` is deliberately **not** in the table. It is a second wording
of `thank_you` — the sentence used when there is no next instalment to name — not
a notification kind, and there is one row per kind on the wire.

| `GET /admin/templates` | → `{items:[{kind, sw, en, variables:[…], locked, version, updated_by, updated_at, segments:{sw,en}}]}`, ordered by kind. |
| `PUT /admin/templates/{kind}` | `{sw, en}` — **both required** (a kind worded in one language would send half the platform's renters a blank message). > **480** characters → 400; a placeholder outside the kind's `variables` → 400 naming it; control characters → 400. → **`200 {template, segments:{sw,en}, warnings?}`**; `warnings` appears when either language runs past three segments. Writes the **previous** body to `platform_template_versions` and bumps `version`. Audited `platform_template.update`. |
| `PATCH /admin/templates/{kind}` | `{locked:bool}` → `200 {template}`. Audited `platform_template.lock`. |
| `POST /admin/templates/{kind}/preview` | `{language:"sw"\|"en", sample?:{var:value}, sw?, en?}` → `200 {kind, language, body, segments, encoding:"gsm"\|"ucs2"}`. `sw`/`en` preview wording that has not been saved yet, so the editor can price a sentence as it is typed; `sample` fills placeholders, defaulting to a representative Tanzanian tenancy. An unknown `language` → 400. |
| `GET /admin/templates/{kind}/versions` | → `{items:[{version, sw, en, admin_name, created_at}]}`, newest first. History holds the bodies that were **replaced**, so version *N* reads "this is what version N said". |
| `POST /admin/templates/{kind}/revert` | `{version}` → `200 {template, restored_from}`. The old wording comes back as a **new** version rather than by rewinding the counter. An unknown version is a 404. |

An unknown `{kind}` is a **404** on every route above.

`variables` is derived from the kind's own sentence plus the eight an org may use
anywhere. `otp` is the exception — `{{code}}` is the only placeholder it offers.

Migration `000016` seeds the eleven code-catalogue kinds — `contract_ready`,
`contract_terminated`, `link_approved`, `link_rejected`, `otp` (locked),
`overdue_daily`, `reminder_7d`, `reminder_due`, `thank_you`, `unsigned_reminder`,
`welcome` — byte-identical to their Go defaults.

#### The lock

`platform_templates.locked` freezes a kind against org overrides. **`otp` ships
locked**: a landlord rewording the message that lets somebody into their account
is a phishing surface.

| `GET /org/notification-settings` | gains `locked_kinds:[…]` and `platform_templates:{kind:{sw,en}}` — the wording every kind falls back to, so the screen can show a locked kind read-only rather than offering an editor whose save will be refused. **`otp` appears in neither**: it is not org-overridable at all, so it is invisible to a landlord rather than read-only. |
| `PUT /org/notification-settings` | an override of a locked kind → **409 `template_locked`**, refused before anything is merged so the save is never half-applied. **Clearing** an override (a null value, or two blank bodies) is always allowed: it moves the org *back* to the platform's wording, which is what the lock protects. |

### `GET /admin/metrics`

The `sms` block gains `credits_used_today` (debited credits since midnight),
`orgs_under_watermark` and `held_total`, beside the existing `sent_24h`,
`failed_24h` and `queued`.

### Part 2 audit actions

`payment_period.recommend` · `expense_category.create` · `expense_category.update`
· `expense_category.delete` · `expense.create` · `expense.update` · `expense.void`
· `expense.receipt_attach` · `expense.receipt_remove` · `branding.theme_update` ·
`user.locale_update` · `sms_credits.topup` · `sms_credits.adjust` ·
`sms_credits.watermark_update` (entity `org_sms_credits`, carrying the target
org) · `platform_template.update` · `platform_template.lock` ·
`platform_template.revert` (entity `platform_template`, **no `org_id`** — the
wording belongs to the platform and an edit changes it for every tenant at once).

### Part 2 migrations

| migration | adds |
| --- | --- |
| `000012_part2_foundations` | the partial unique index on `payment_periods`, `users.locale`, `expense_categories`, `expenses`, `org_themes`, `platform_templates` (+`locked`), `platform_template_versions`, `org_sms_credits`, `sms_credit_ledger` (append-only trigger), `notification_log.status = 'held_no_credit'`. Compose init adds the `receipts` bucket. |
| `000013_expense_receipts` | `expenses.receipt_content_type`, `expenses.receipt_size`. |
| `000014_report_indexes` | the three report indexes above. |
| `000015_language` | `notification_log.language` (`NOT NULL DEFAULT 'sw'`, checked `IN ('sw','en')`), `contract_templates.body_html_sw` (`NOT NULL DEFAULT ''`, seeded as described), `contracts.language` (`NOT NULL DEFAULT 'en'`, checked `IN ('sw','en')`). |
| `000016_platform_templates_seed` | the eleven platform template rows. |

### Deviations from the planned contract

The Part 2 plan was written before the code. Where they disagree, this is what
shipped and why (rows also in DECISIONS.md):

| planned | shipped | why |
| --- | --- | --- |
| `GET /themes/presets` → `{items:[…]}` | **`{presets:[…]}`** | the payload is a catalogue of presets, not a page of a listing |
| invalid `locale` → 422 | **400** | codebase-wide validation convention: a bad field value is a 400 with `errors` |
| bulk send → `queued:{sw,en}` | **`queued` scalar + `by_language:{sw,en}`** | keeps the Phase 6 `{queued, skipped}` shape; the per-language counts are additive, not a breaking re-type |
| topup/adjust → `{balance}` | **`{balance, released}`** | the caller wants to know how many held messages the top-up let go |
| `PUT /admin/templates/{kind}` → `{template}` | **`{template, segments, warnings?}`** | the editor prices the sentence it just saved without a second call |
| `otp` shown as a locked kind to landlords | **`otp` never appears** in `locked_kinds` or `platform_templates` | it is not org-overridable at all, so a landlord has nothing to see read-only |
| out-of-credit rows `failed` | **`held_no_credit`** | nothing went wrong with the message; `failed` would invite retries that cannot succeed |
| `> 400 buckets` → 400 | **422 `too_many_buckets`** | the request parses; it is the window that cannot be served. Unresolvable windows are 422 too; malformed values stay 400 |
| `{{rent}}` = the unit price | **`rent_per_period`** (unit price × payment period ÷ rent period) + `{{rent_basis}}` for the price | the signed document was stating the wrong figure (PLAN2 scope, found in the end-to-end test) |
| contract language implicit | **`contracts.language`** on every DTO, defaulting to the renter's locale | the document a person signs should be in the language they read |
| one occupancy number | **`occupancy_pct` is 0–100**, `collection_rate` and the Phase 7 `occupancy_rate` are 0–1 | the field names say which is which; a client that reads both must not scale them alike |
| org theme presets duplicated in `packages/ui` | backend `presets.json` is the **source of truth**; the UI copy is generated and pinned by `TestPresetsMatchUICopy` | frontends need a fallback before the API answers; drift must fail the build |
| §16.3 `items:[{schedule:{…}, renter_name, …}]` | the **schedule fields flattened** into the item beside the identity fields | the row is one thing a landlord reads; a nested object for six fields buys nothing and the Phase 5 `schedule` shape is already flat everywhere else |
| §16.3 sorted by `due_date` then `id` | `due_date`, then **unit name**, then id | on a day with several instalments the landlord reads the board by unit; an id is not an order anybody can see |
| §16.4 `PUT /org/bank-account {bank_account?, mobile_money?}` | the **Phase 5 flat body** plus an optional `mobile_money` key | re-nesting the four bank fields would break every existing client for no gain; the wallet is additive |
| §16.4 `GET /public/units/{unit_code}` carries the payment blocks | **not shipped** — the blocks are on `GET /org/bank-account` and `GET /me/schedules` only | publishing an account number on an unauthenticated QR endpoint invites payment-redirect fraud; a renter sees the instructions once they are linked, which is when they owe anything |
| §16.4 audited `org.update` | audited **`org.bank_account_update`** | the action the Phase 5 endpoint already writes; before/after now carry both blocks |

### Phase 15 — hardening

No new endpoints. Reports v2 routes enter `make loadtest` (p95 < 300 ms on the
seed org), the isolation census covers every Part 2 route (the build fails on an
uncovered one), and receipt uploads (30/h/org) and admin template edits are rate
limited.

---

## Part 2 — Phase 16 (planned)

**Shipped 20 Sep 2026.** Each sub-heading below is the contract as implemented ([PLAN2.md](PLAN2.md) §16.1–§16.4; SPEC §4 ledger rules, §5.7, §5.9, §5.14, §7; FLOWS 7 and 14). Where a lane deviated from the plan it says so under "Differences from the planned contract".

Phase 16 keeps every Part 2 convention: audience cookies, RFC-7807 problems with
the machine-readable code in `type`, **400** for a malformed field with `errors`
populated, **404** for an id outside the caller's scope, **409** for a business
conflict, and an audit row on every mutation. New codes: `contract_not_active`,
`proof_not_pending`, `batch_not_previewed`, `batch_committed`, `undo_expired`.

### 16.1 Proofs

**Shipped.** Audience **renter** (`tms_r`) for `/me/proofs*`, **org** (`tms_o`,
owner + manager) for `/proofs*`. Bucket `proofs`, key
`{org_id}/{proof_id}.{jpg|png|pdf}` — both segments are ids the server minted,
so no request can steer the key.

`proof` shape, identical on every route that returns one:

```json
{"id":"…","contract":{"id":"…","unit_name":"A2","property_name":"Mikocheni Flats","renter_name":"Asha Mrisho","renter_user_id":"…"},
 "schedule_id":"…|null","amount":250000,"paid_at":"2026-09-19T08:00:00Z",
 "method":"bank_transfer|mobile_money_manual","reference":"TRF-99|null","note":"…|null",
 "content_type":"image/png","size_bytes":20480,
 "status":"submitted|accepted|rejected","payment_id":"…|null",
 "reviewed_at":"…|null","reviewed_by_name":"…|null","rejection_reason":"…|null",
 "created_at":"…","view_url":"https://…"}
```

`view_url` is present only on `GET /proofs/{id}`, where one was issued.

| Route | Contract |
| --- | --- |
| `POST /me/proofs/upload` | `{contract_id, content_type:"image/jpeg"\|"image/png"\|"application/pdf", size_bytes(1…5 MiB)}` → `200 {proof_id, upload_url, object_key, expires_in:900, headers:{"Content-Type":…}}`. The `proof_id` is minted here and becomes the row's id on submission. The contract must be the caller's own and `active\|expiring` — another renter's id is a **404**, an unsigned or finished one a **409 `contract_not_active`**. Type and size are checked before MinIO is consulted (400 naming `content_type` / `size_bytes`). 30 links per renter per hour. MinIO down → 503; the ticket store (Redis) down → 503. |
| `POST /me/proofs` | `{contract_id, schedule_id?, amount(int >0), paid_at, method, reference?(≤80), note?(≤500), object_key}` → **`201 {proof}`** with `status:"submitted"`. The completion-callback pattern of `/expenses/{id}/receipt/complete`, plus a one-shot ticket: `object_key` must be one this renter was issued for this contract, and the object MinIO holds must match the ticket's size and content type exactly, or it is deleted and the answer is a **400** naming `object_key`, `size_bytes` or `content_type`. `schedule_id` must belong to the same contract (else 404). `paid_at` is RFC3339 and no more than a day in the future. Rate limited to **10 per renter per rolling 24 h** (429 with `Retry-After`), enforced in Redis and again in Postgres so a cold cache cannot lift the ceiling. **No SMS** is sent. Audited `proof.submit`. |
| `GET /me/proofs?cursor=&limit=` | → `{items:[proof], next_cursor}`, newest first, the caller's own proofs across every org they rent from, each carrying `rejection_reason` when rejected. Shared `(created_at, id)` cursor encoding. |
| `DELETE /me/proofs/{id}` | → **204**, withdraw. Only while `status:"submitted"` — an answered proof is a record and stays (**409 `proof_not_pending`**). The row and the object are both removed. Audited `proof.withdraw`. |
| `GET /proofs?status=&cursor=&limit=` | → `{items:[proof], next_cursor, status}`. `status` is `submitted\|accepted\|rejected`, default `submitted`; ordering is **`created_at` ascending — the oldest claim first** (the queue is worked forward, unlike the org's other listings). |
| `GET /proofs/summary` | → `{submitted_count}`. The badge is its own endpoint rather than a field on the listing, because it is drawn on every landlord screen including the ones that never list a proof. It reads the partial index and nothing else. |
| `GET /proofs/{id}` | → `{proof}` with `view_url` — a presigned read, **TTL 300 s**, shorter than a receipt's because the file is a renter's banking screenshot. Issuing it is audited `proof.view`, the way `kyc.view` is. |
| `POST /proofs/{id}/accept` | `{amount?, schedule_id?, paid_at?, allow_overpay_rollover?}` → `200 {proof, payment, schedules:[…]}`. Runs the **same allocator `POST /payments` runs**, with the proof's own fields as defaults and the landlord's corrections applied (both readings recorded in the audit row); links `payments.id` onto `payment_proofs.payment_id`, sets `status:"accepted"` with `reviewed_by_name`/`reviewed_at`, and queues the existing `thank_you` SMS. The allocator's **409 `overpay_confirm_required`** (with `excess` and `next_schedule`), **409 `exceeds_contract_balance`**, **409 `contract_not_active`** and **404** for a schedule outside the contract propagate unchanged, so the landlord's confirm sheet is reused without a second code path. Not `submitted` → **409 `proof_not_pending`**. Audited `proof.accept` beside the allocator's own `payment.record`. |
| `POST /proofs/{id}/reject` | `{reason(1–200)}` → `200 {proof}` with `status:"rejected"`, `rejection_reason`, `reviewed_by_name`, `reviewed_at`. Queues **`proof_rejected`** (new kind, SW/EN, platform template seeded by migration 000018 + org override, `{{reason}}` platform-only as `contract_terminated` has it), debited like any other kind. Not `submitted` → **409 `proof_not_pending`**. Audited `proof.reject`. |

**Differences from the planned contract above:** the conflict code is
`proof_not_pending` (not `proof_not_submitted`); the badge is `GET
/proofs/summary` rather than a `submitted_count` field on the listing; the proof
shape carries `renter_name`/`renter_user_id` inside `contract` and flat
`content_type`/`size_bytes`/`reviewed_by_name` rather than nested `renter`,
`file` and `reviewed_by` blocks; `GET /proofs/{id}` returns no `schedule` block
(the schedule is already on `GET /contracts/{id}/schedules`); the read TTL is
300 s; `POST /me/proofs/upload` also returns `proof_id`, and an upload is bound
to a one-shot ticket in Redis rather than to the key's shape alone.

`POST /payments/{id}/reverse` on a payment that came from a proof leaves the
proof **`accepted`** with the payment stamped reversed: the claim was made and
answered, and the reversal is a fact about the payment (SPEC §4 ledger rules).

### 16.2 Imports

**Shipped.** Audience **org** (`tms_o`), owner + manager. `kind` is
`units|renters|payments`. An import is two movements: a **preview** that writes
nothing but a record of the file, and a **commit** that runs every ok row
through the same service functions the manual endpoints use, inside one
transaction — a landlord never ends up with half a spreadsheet.

| route | behaviour |
| --- | --- |
| `GET /imports/templates/{kind}.csv` | → `text/csv` attachment (`filename="tms-import-{kind}.csv"`): the header row plus one example line. Headers are **fixed English machine names** — never the SW/EN screen labels. Without the `.csv` suffix the same path answers `200 {kind, columns:[{name, required, example, help}]}`, the column reference the screen prints. Unknown kind → 404. |
| `POST /imports/preview` | multipart `file` + form field `kind` → **`201 {batch, rows:[…]}`** (shapes below). File ≤ **2 MiB** and ≤ **5 000 data rows**; UTF-8 with or without BOM; delimiter `,` or `;` sniffed from the header line; headers matched case-insensitively and trimmed. A header that does not match → **400 `csv_header_mismatch`** with `missing:[…]` and `unknown:[…]`. Other file refusals carry `type`: `empty_file`, `not_utf8`, `too_many_rows`, `file_too_large`, `unreadable_csv`. **Nothing is written but the batch** (`status:"previewed"`) and its rows. Rate limited to **20 per hour per org** (429). Audited `import.preview`. |
| `POST /imports/{batch_id}/commit` | `{skip_errors?:bool}` (body optional) → `200 {batch, created:{units, properties, renters, contracts, payments}}`. With `error_count > 0` and `skip_errors` absent/false → **409 `batch_has_errors` `{error_count}`**; with `skip_errors:true` the bad lines are dropped and the rest commit. The file is **re-resolved inside the transaction**: a row that passed the preview and no longer does → **409 `row_failed` `{line, column, reason}`** and nothing is written. A batch that is not `previewed` → 409 `already_committed` / `already_undone`. Each committed row records what it became (`entity_type`, `entity_id`). Audited `import.commit`. |
| `POST /imports/{batch_id}/undo` | → `200 {batch, undone:{payments, contracts, units, properties, renters}}`, within **24 h** of `committed_at` (else **409 `undo_window_closed`**; a batch that was never committed → 409 `not_committed`, one already undone → 409 `already_undone`). Audited `import.undo`. |
| `GET /imports?cursor=&limit=` | → `{items:[batch…], next_cursor}`, newest first. |
| `GET /imports/{id}` | → `{batch, rows:[…]}` — the preview table as stored, with each row's `entity_type`/`entity_id` after a commit. |

```jsonc
// batch
{ "id": "…", "kind": "units", "filename": "previous-units.csv",
  "row_count": 5, "ok_count": 2, "error_count": 3,
  "status": "previewed",                       // previewed | committed | undone
  "created_by": {"user_id": "…", "name": "Joseph Chuchu"},
  "committed_at": null, "undone_at": null,
  "can_undo": false,                           // committed and inside the 24 h window
  "created_at": "2026-09-19T20:11:03Z" }

// row  (errors is null when the row is ready to commit)
{ "line": 3,                                   // the line of the file, header = 1
  "raw": {"property": "Block A", "unit": "A2", "rent_amount": "150000"},
  "errors": {"unit": "this property already has a unit with this name"},
  "resolved": {"property": "Block A", "property_create": false, "unit": "A2",
               "rent_amount": 150000, "rent_period_days": 30, "status": "vacant"},
  "entity_type": null, "entity_id": null }
```

`errors` is an object keyed by **column name**; the key `_row` carries a problem
with the whole line (a record with the wrong number of cells). `resolved` is
what the row will hit or create — names for the preview table, ids after the
commit.

Columns per kind, validated per row with the rules of the manual endpoints:

| kind | columns | notes |
| --- | --- | --- |
| `units` | `property, unit, rent_amount, rent_period_days?, status?` | property matched by name within the org (case-insensitive, trimmed) and **created when missing**, flagged `resolved.property_create:true`; `rent_period_days` defaults to the org's recommended payment period, else 30; `status` is `vacant` (default) or `unlisted`. A unit name the property already has — or that an earlier line of the same file claims — is a row error. `rent_amount` accepts `TZS 150,000` and a trailing `.00`. |
| `renters` | `full_name, phone, locale?, property?, unit?` | phone normalised like registration (`+255…`); an existing account with that number is **attached to the org, never duplicated**; a new one is pre-registered with no PIN and claims itself through the ordinary OTP sign-in. With a `unit`, an `approved` `unit_link_requests` row and, through the same `onLinkApproved` hook the Approve button runs, a contract at **`pending_signature`** from the org default template (term 365 days, starting today), its `contract_ready` SMS queued **on commit only**. **No import ever activates a contract**, and the unit stays vacant. Without a `property` the unit name must be unique across the org. |
| `payments` | `unit, renter_phone, amount, paid_at, method, reference?, note?` | contract resolved by unit + renter phone, newest in status `active\|expiring\|ended\|terminated` — history may belong to a finished tenancy. `paid_at` takes `YYYY-MM-DD` or a date-time, and may not be more than a day ahead; payments predating the contract start are **not** special-cased (PLAN2 open question 6). Rows are allocated by the existing allocator in **`paid_at` then line order** with `allow_overpay_rollover=true`; the preview simulates the same order, so a row that would exceed the contract balance is a row error `exceeds_contract_balance` on `amount`. `method` is limited to `cash\|bank_transfer\|mobile_money_manual`. Committed payments carry `import_batch_id`, which the `payment` shape now returns (null for money keyed in by hand) so ledgers can show an "imported" chip. No thank-you SMS is sent for historical money. |

**Undo semantics.** Payments of the batch are reversed through the existing
reverse path with reason `import undone` (the rows stay, stamped `reversed`).
Contracts, units, properties and renter accounts the batch created are taken
back **only when untouched since**, which means simply: nothing references
them — a contract with any payment stays, a unit with any contract stays, a
property with any live unit stays, and a renter account stays the moment it
holds a contract or a link request in the org, or has ever been signed into
(`pin_hash`/`password_hash` set). Anything kept is simply absent from the
`undone` counts.

Cells beginning `=`, `+`, `-` or `@` are stored **verbatim** and neutralised on
every export, exactly as the payment-status and expenses exports do.

### 16.3 Upcoming / next-due fields

**Shipped.** Every countdown on the platform is counted on the Dar es Salaam
wall clock (`internal/tz`), never on `CURRENT_DATE`: a schedule due today reads
`0` from 00:00 EAT, not from 03:00.

| `GET /reports/upcoming?days=7\|14\|30&property_id=` | org audience → `{items:[schedule + {renter_name, renter_user_id, phone, unit_name, property_name, days_until_due}], total_due, count, days, window:{from,to}}`. The item is the **Phase 5 `schedule` shape flattened** — `id, contract_id, period_start, period_end, due_date, amount, paid_amount, status, days_overdue` — with the identity fields beside it (no nested `schedule` or `contract` object). Sorted by `due_date`, then unit name, then id. `days` defaults to **14**; anything else is a **400** with `errors.days`. Rows are the unsettled ones (`pending\|partial\|overdue`) on `active\|expiring` contracts with `due_date` in `[today, today+days]`; `waived`, `paid` and finished tenancies are excluded. `total_due` is the sum of `amount − paid_amount`. `property_id` outside the org → **404**. The org's overdue sweep runs first, as on `GET /schedules`. |
| `GET /reports/summary` | gains **`upcoming_7d:{count,total}`** — the dashboard `upcoming` card, always a 7-day window whatever the summary's period, so the card costs no second call. |
| `GET /me/schedules` | every item row gains **`days_until_due`** (0 today, negative when past; `days_overdue` is unchanged and still floored at 0). `next_due` is the same object, so it carries it too, and additionally **`proof:{id,status}\|null`** — the caller's newest proof still `submitted` against that instalment. The `bank_account` block gains `mobile_money`, and a top-level `mobile_money` rides beside it (16.4). |
| `GET /renters` | rows gain `next_due_date\|null`, `next_due_amount\|null` and `overdue_amount` (0 when none), aggregated over the renter's running tenancies with this org. They are computed from the **same three queries** as `/reports/payment-status` (`ReportTenancies`, `ReportContractBalances`, `ReportNextDue`), so the column and the report cannot drift; a reconciliation test asserts it. `GET /renters/{user_id}` carries the same three fields on its `renter` block. |
| `GET /units` | occupied rows gain `next_due_date\|null` — the next unsettled due date on the unit's running contract. Vacant, unlisted, maintenance, and occupied units with nothing outstanding carry `null`. |
| `PUT /org/branding {dashboard_prefs}` | accepts the card ids **`proofs`** and **`upcoming`** alongside the Phase 9–14 set; `upcoming` sits directly after `overdue` in the allowlist order. Unknown ids stay a 400. |

### 16.4 Payment instructions

**Shipped.** `mobile_money` lives beside `bank_account` in `orgs.settings` as a
top-level key, so both round-trip through `PATCH /org` untouched.

| `GET /org/bank-account` | → `{bank_account:{bank_name, account_name, account_number, instructions, mobile_money:{provider,number,name}\|null}\|null, mobile_money:{provider,number,name}\|null, payment_instructions_set:bool}`. The wallet appears **twice on purpose**: inside the account block, which is what the renter's card renders, and beside it, because an org may take mobile money and no bank transfer. `payment_instructions_set` is true when either block is set — the flag behind the landlord's setup nudge. |
| `PUT /org/bank-account` | the Phase 5 body (flat `bank_name`, `account_name`, `account_number`, `instructions`) plus optional **`mobile_money`**: an object replaces the wallet, an explicit `null` clears it, and **omitting the key keeps what is stored** (so editing bank details never silently drops the wallet). `provider` ≤ 40, `number` ≤ 20, `name` ≤ 120, all three required when the object is present → else 400. Response is the `GET` shape. Audited `org.bank_account_update`, before/after carrying both blocks. |
| `GET /me/schedules` | `bank_account` gains `mobile_money`, and `mobile_money` rides beside it. Both are the org behind `next_due`, and both are `null` when the renter owes nothing or the landlord has set neither. |
| org SMS template whitelist | gains **`{{pay_link}}`** → `{PUBLIC_BASE_URL or APP_BASE_URL}/enduser/payments`. Nine variables now: `name amount due_date property unit org next_due_date link pay_link`. The platform defaults of `reminder_7d`, `reminder_due` and `overdue_daily` end with it in SW ("Lipa hapa: …") and EN ("Pay here: …"). Unknown variables are still a 400. Installations seeded by `000016` are updated **without a migration**: `SeedPlatformTemplates` (which already runs at every startup) re-applies the new sentence to any row still holding the previous wording byte-for-byte at `version = 1`, and refreshes every kind's `variables` column so the admin editor's chips offer `pay_link`. An admin's own wording is never touched. |

**Phase 16 audit actions:** `proof.submit` · `proof.withdraw` · `proof.accept` ·
`proof.reject` · `proof.view` (entity `payment_proof`) · `import.preview` ·
`import.commit` · `import.undo` (entity `import_batch`).

## Part 2 — Phase 18 (planned): landlord-assisted onboarding

Audience org (`tms_o`), owner + manager, unless noted. FLOWS 2b, SPEC §5.15. Errors carry the code in the RFC-7807 `type` field as elsewhere.

| Endpoint | Contract |
|---|---|
| `POST /assist` | `{phone, unit_id}` → `201 {session:{id, org_id, unit_id, unit_code, unit_name, phone, purpose:"register"\|"login", status:"open", code_issued_count, expires_at, created_at}, code, code_expires_at, link}`. `purpose` is `login` when the phone already belongs to a renter, `register` otherwise. `link = {APP_BASE_URL}/enduser/u/{unit_code}?assist={session.id}`. 409 `assist_open {session_id}` if the org already has an open session for that phone; 409 `not_a_renter_phone` if the phone belongs to an org user or admin; 404 unit; 429 per-org `assist:issue` 30/h. Writes the code to the SMS path's Redis slot (**no SMS**), a `notification_log` row `kind=otp, channel=in_person, status=shown`, audit `renter.assist_start` + `renter.assist_code`. |
| `GET /assist` | `{items:[session…]}` open sessions of the org, newest first. |
| `GET /assist/{id}` | `{session, status_detail:"waiting"\|"registered"\|"requested"\|"approved"\|"closed", renter:{id, full_name}?, link_request:{id, status}?}`. Polled by the landlord screen. Never returns the code. |
| `POST /assist/{id}/code` | New code replacing the slot, TTL reset to 5 min, session `expires_at` extended to now + 30 min → `200 {code, code_expires_at, code_issued_count}`. 409 `assist_closed` when closed/expired; 429 after 10 codes per session or the org budget. Audit `renter.assist_code {n}`. |
| `POST /assist/{id}/close` | → `200 {session}` status `closed`. Idempotent. Audit `renter.assist_close`. |
| `POST /contracts/{id}/witness-otp` | Contract must be `pending_signature` with no renter signature (same 409s as `sign-otp`). Writes the sign slot (`otp:sign:{contract}:{phone}`) and a witness marker (TTL 5 min) → `200 {code, code_expires_at}`. Counts against the org `assist:issue` budget. Audit `contract.witness_otp`. The renter's `POST /contracts/{id}/sign` is unchanged; when the marker is live the signature row gets `witnessed_by_user_id`, and the document's signature block reads "witnessed by {name}". |
| `GET /public/assist/{id}` | Unauthenticated → `200 {unit_code, purpose, status:"open"\|"closed"}`. 404 unknown. No phone, no org fields. |

Hooks in existing endpoints (no contract change): `POST /auth/otp/verify` (`register`, `login`) and `POST /auth/register/renter` stamp `assist_sessions.renter_user_id` when an assist marker exists for the phone and add `assist_session_id` to their audit `after`; `POST /units/{unit_code}/link` stamps `link_request_id` on the open session for that org + renter + unit; `GET /contracts/{id}` and `/document` expose `signatures[].witnessed_by:{id, full_name}|null`.

**Phase 18 audit actions:** `renter.assist_start` · `renter.assist_code` · `renter.assist_close` (entity `assist_session`) · `contract.witness_otp` (entity `contract`).

**Phase 18 migration:** `000020_assist_sessions` — `assist_sessions` table, `contract_signatures.witnessed_by_user_id`, `notification_log.channel` CHECK widened to `('sms','in_person')`, `status` CHECK gains `shown`.

**Deviations (Phase 18 backend, 20 Sep 2026)** — implemented as written above except:

| Endpoint | Deviation | Why |
|---|---|---|
| `GET /assist` | Each item is the full `GET /assist/{id}` shape (`{session, status_detail, renter, link_request}`), not a bare `session`. | The landlord's list screen shows the same status line as the detail screen; returning it inline saves one fetch per row on a 5 s poll. Additive — a `session` object is still inside each item. |
| `POST /assist/{id}/code` | Response carries `expires_at` (the session's extended window) beside `{code, code_expires_at, code_issued_count}`. | The refresh is what pushes the 30-minute window out, so the countdown the screen is already showing has to be told. |
| all session reads | A session past `expires_at` reports `status: "closed"` (and `status_detail: "closed"`) although the stored column is still `open`. | No sweep runs; expiry and closure are one event from the landlord's screen, and the partial unique index on `(org_id, phone) WHERE status='open'` is released by the `expires_at > now()` filter every lookup carries. |
| `POST /contracts/{id}/witness-otp` | Also writes a `notification_log` row (`kind=otp, channel=in_person, status=shown`, empty body, `dedupe_key = assist:witness:{contract_id}:{nonce}`). | A witnessed signing code is a revealed code like any other; leaving it out of the delivery log would make the one reveal that binds a contract the one reveal an operator cannot find. The key carries a random nonce because a contract has no running count to key on. |

---

## Part 2 — Phase 19 (planned): NIDA reveal, platform user directory, name corrections

Conventions are Part 2's: audience cookies (`tms_o` org, `tms_r` renter, `tms_a`
platform admin), RFC-7807 problems with the machine-readable code in `type`,
**400** for a malformed field with `errors` populated, **404** for an id outside
the caller's scope, **409** for a business conflict, **422** for a request that
parsed but cannot be honoured, and one audit row per mutation. New codes:
`last_owner`, `renter_signed_elsewhere` (Phase 21; `renter_signed` retired).

### 19.1 NIDA reveal (landlord)

A reveal is a **POST**, never a query flag on a GET: the number must never sit in
a URL, a browser cache, a prefetch or a log line, and the audit row must be
unmissable.

| Endpoint | Contract |
|---|---|
| `POST /renters/{user_id}/nida/reveal` | Org audience, **owner + manager**. Body `{reason?: string ≤200}` (body may be absent). → `200 {nida_number, full_name, revealed_at}`. The renter must have a relationship with the caller's org — a `unit_link_requests` row in **any** status, or any contract, in this org — otherwise the same **404 `not found` / "no such renter"** the rest of the renter directory gives, so the endpoint never confirms an account exists elsewhere on the platform. A renter whose profile holds no NIDA → **404** with detail "no NIDA on file". Rate limited **60 per hour per org** (`nida:reveal`, 429 with `Retry-After`) so bulk scraping is loud. Audited `renter.nida_reveal`, entity `user`, org scope, `after:{reason?, actor_kind:"org_user"}` — **never** the number. |
| `POST /admin/users/{id}/nida/reveal` | Platform admin. Body `{reason: string 1…200}` — **required** here (support cases are justified in writing). Same 200 shape, same 404s, no relationship check, no org limiter (the limiter key is the admin's own user id, 60/h). Audited `renter.nida_reveal` with `after.actor_kind:"platform_admin"` and **no** `org_id`. |
| `GET /me/profile` | Gains **`nida_reveals: [{at, by_kind:"landlord"\|"platform_admin", org_name?}]`** — the renter's own list of who looked, **last 10**, newest first, read from `audit_log`. `org_name` is present for a landlord reveal and absent for a platform one. `reason` is **not** exposed to the renter. |

The masked value (`nida_masked`, `••••••••1234`) on every existing endpoint is
unchanged; nothing else ever returns the full number.

### 19.2 Platform user directory (admin)

Audience **platform admin** (`tms_a`); an org user or renter gets the usual 401/403.

| Endpoint | Contract |
|---|---|
| `GET /admin/users?q=&kind=&status=&org_id=&cursor=&limit=` | → `{items:[user_row], next_cursor}`, newest first, shared `(created_at, id)` cursor encoding, `limit` 1…100 (default 25). `q` (≤120) matches **phone normalised the way registration normalises it** (`0755…` → `+255755…`), **e-mail lower-cased exact-prefix**, and **full name `ILIKE` prefix** — wildcards in the value are escaped. `kind` is `renter\|org_user\|platform_admin`, `status` is `active\|suspended`, `org_id` narrows to users connected to that org (members for `org_user`, link requests or contracts for `renter`). **No NIDA field of any kind, masked or full, appears on the list.** |
| `GET /admin/users/{id}` | → the aggregate below. Reading it is audited **`admin.user_view`** (entity `user`, no org): the page assembles cross-org PII, so the read is a recorded event. Unknown id → 404. |
| `POST /admin/users/{id}/suspend` | `{reason: string 1…200}` → `200 {user}` with `status:"suspended"`; **every session of that user is revoked**, the same way `POST /admin/orgs/{id}/suspend` revokes an org's. Already suspended → 409 `already_suspended`. Audited `admin.user_suspend`. |
| `POST /admin/users/{id}/activate` | `{reason: string 1…200}` → `200 {user}` with `status:"active"`. Already active → 409 `already_active`. Audited `admin.user_activate`. |

```jsonc
// user_row  (the list row; the detail's `user` is the same shape plus nida_masked)
{ "id": "…", "kind": "renter",               // renter | org_user | platform_admin
  "full_name": "Asha Mrisho", "phone": "+255755000111", "email": null,
  "status": "active",                        // active | suspended
  "created_at": "2026-09-01T07:12:00Z", "last_seen_at": null,
  "orgs": [{"id":"…","name":"JJnE Rentals",
            "role": null,                    // org_user only: owner | manager
            "relationship": "renting"}],     // renter only: renting | applied | past
  "contracts_live": 1,                       // renter only
  "kyc_status": "verified" }                 // renter only: none|submitted|verified|rejected

// GET /admin/users/{id}
{ "user": { …user_row…, "nida_masked": "••••••••1234" },   // renter only, null when none
  "link_requests": [ … last 10 Phase 3 link_request shapes, with org_name … ],
  "contracts":     [ {"id","org_id","org_name","unit_name","property_name","status",
                      "start_date","end_date","rent_amount"} … all … ],
  "payments":      {"count": 12, "total": 1800000, "last_paid_at": "2026-09-01"},
  "memberships":   [ {"org_id","org_name","org_status","role","created_at"} … ],  // org_user
  "audit":         {"items": [ …last 50 rows where this user is actor OR entity… ],
                   "next_cursor": null} }
```

`GET /admin/users/{id}?audit_cursor=` pages the audit block alone, reusing the
`GET /admin/audit-log` row shape and cursor. The admin audit filter's `action`
allow-list gains `renter.nida_reveal`, `admin.user_view`, `admin.user_suspend`,
`admin.user_activate` and `admin.user_update`.

### 19.3 Name corrections

Every rename writes **`users.full_name`**, and for a renter **`renter_profiles.full_name`**
as well, in one transaction with its audit row (`before`/`after` carrying the two
names). Bounds are the profile's: **2…80 characters after trimming**, collapsed
inner whitespace; anything else is a **400** naming `full_name`.

| Endpoint | Contract |
|---|---|
| `PATCH /org/members/me` | Org audience, any role, the caller's own row. Body `{full_name?, locale?}` — **the Phase 13 locale-only body still works unchanged**, and either key may be sent alone. → `200 {member}`. Audited `member.update` (a locale-only call keeps writing `user.locale_update`, as before). |
| `PATCH /org/members/{id}` | Org audience, **owner only**. `{full_name?, role?}`, at least one. `role` is `owner\|manager`. Cannot change **own** role (409 `cannot_change_own_role`), cannot demote the **last owner** (409 `last_owner`). A member id outside the org → 404. → `200 {member}`. Audited `member.update`. |
| `PATCH /renters/{user_id}` | Org audience, **owner + manager**. `{full_name}`. Relationship check and 404 are `GET /renters/{user_id}`'s. `reason` (≤200) optional before any signature, **required once the renter has signed with this org** (400 on `reason`; Phase 21 — previously 409 `renter_signed`). Refused when the renter has signed a contract with **another** org → **409 `renter_signed_elsewhere`** with detail "ask the renter to correct it in their Profile". After a signature the `name_corrected` SMS is sent even if the org disabled the kind; the audit `after` carries `reason` and `after_signature: true`. Signed documents keep their snapshot. → `200 {renter, profile}` (the `GET /renters/{user_id}` blocks). Audited `renter.update`. Queues the renter SMS **`name_corrected`** (new kind, SW/EN, org-overridable, credits apply, disable-able in Notification settings). |
| `PATCH /admin/users/{id}` | Platform admin, any user kind, **no** signed-contract restriction. `{full_name, reason: string 1…200}`. → `200 {user}` (the `user_row` shape). Audited `admin.user_update` with `before`, `after` and `reason`. Notifies the renamed account the same way: `name_corrected` SMS for a renter, the existing mailer for an org user or admin. |

**The contract document is untouched by every one of these.** The rendered terms
are a snapshot (`{{renter_name}}` frozen at creation and covered by the hash), so
`GET /contracts/{id}/document` and `GET /contracts/{id}/verify` return the same
bytes and the same hash after a rename; only the live `parties.*.name` on the
list/detail views follows the account. A test pins the bytes and the hash.

**Phase 19 audit actions:** `renter.nida_reveal` (entity `user`) · `admin.user_view` ·
`admin.user_suspend` · `admin.user_activate` · `admin.user_update` (entity `user`) ·
`member.update` (entity `org_member`) · `renter.update` (entity `user`).

---

## Part 2 — Phase 20 (planned): proof amount lock, shell logo, payment backfill

### 20.1 Proof of payment: locked amount

| `POST /me/proofs` | Unchanged except: when **`schedule_id` is given**, `amount` must equal that instalment's outstanding balance (`amount − paid_amount`) at submit time, or the answer is **422 `amount_mismatch`** with `{"expected": 150000}` beside the RFC-7807 fields. The client re-reads the row and re-submits. Without `schedule_id` the existing rule stands (1 ≤ `amount` ≤ contract balance). A schedule already `paid` or `waived` has an outstanding of 0 and therefore always mismatches. |
|---|---|

20.2 (landlord logo in the app shell) is frontend-only and adds no endpoint;
`GET /org/branding` already carries `logo_url`.

### 20.3 Historical payments backfill

A tenancy that predates TMS is represented **truthfully**: the contract's
`start_date` is the real move-in date, the generator produces the past periods,
and the landlord settles them.

| Endpoint | Contract |
|---|---|
| `POST /contracts` | `start_date` may now be up to **10 years** in the past (`3650` days) for a **landlord-created** contract — the manual form and the `renters` import both. Further back → 400 naming `start_date`. The **renter application** `POST /units/{unit_code}/link` keeps its **7-day** backstop: a renter may not draft a back-dated tenancy alone. Activation generates the full span; past-due rows come out `overdue`, which is the truth until a backfill settles them. |
| `POST /contracts/{id}/backfill` | Org audience, **owner + manager**. `{until: "YYYY-MM-DD", mode: "paid"\|"waived", paid_at?: "YYYY-MM-DD"\|"due_date", method?, reference?, note?}` → `200 {settled, skipped, total, schedules:[…]}`. Closes every schedule row of the contract with `due_date ≤ until` that is still unsettled (`pending\|partial\|overdue`). `paid`: **one payment per row** for that row's outstanding, `source:"backfill"`, `paid_at` defaulting to the row's own `due_date` (`paid_at:"due_date"` states that explicitly; a date applies the same date to every row), `method` defaulting to `cash` and limited to `cash\|bank_transfer\|mobile_money_manual`, `reference` ≤80, `note` ≤500; each payment is audited `payment.record` with `after.source:"backfill"`. `waived`: the rows become `waived` with the note (`note` required, ≤500). Rows already `paid` or `partial`… a `partial` row is settled for its **remainder**; rows already `paid` or `waived` are **skipped and counted**. `total` is the money the call moved (0 for `waived`). Contract not `active\|expiring` → **409 `contract_not_active`**. `until` in the future, or before the contract's `start_date` → **422** naming `until`. Nothing due on or before `until` → `200 {settled:0, skipped:n, total:0}`. One audit `contract.backfill` row carries the counts beside the per-payment rows. Queues **one** `backfill_done` SMS (new kind, SW/EN, `{{org}}`/`{{date}}`, credits apply, disable-able in Notification settings) — **never** one per row. |
| `GET /contracts/{id}/schedules`, `GET /me/schedules` | Each schedule row gains **`last_payment_source`**: `"manual"\|"import"\|"backfill"\|null` — the `source` of the newest live payment allocated to that row, `null` when nothing has been allocated. It is a join, not a stored column. |
| `GET /payments?source=manual\|import\|backfill` | New filter, combinable with the existing ones; an unknown value is a 400 naming `source`. The `payment` shape gains **`source`** beside `import_batch_id` (which stays, and is still what names the batch). The payments CSV export gains a **`source`** column after `reference`. |
| `GET /me/payments` | The renter's own receipt history carries **`source`** too (`"manual"\|"import"\|"backfill"`, the same three values and the same meaning — both listings render from one `payment` shape), so the renter's "backfilled"/"imported" chip has a field to read. It is always present, never null. The `source` **filter** is the landlord's listing only; the renter's history takes no `source` query. |
| `POST /imports/preview` (`kind=payments`) | When a row's `paid_at` falls **before the contract's first schedule period** *and* its `amount` is no more than the **first period's `rent_amount`** (a figure one missing period could have absorbed), the row error on `paid_at` reads "this payment predates the rent book — set the contract's start date to the real move-in date, or use Backfill to settle the periods before TMS" instead of the generic `exceeds_contract_balance`. Allocation itself ignores `paid_at` (it walks from the earliest unpaid period), so a row that predates the book **and** is larger than one period keeps `amount: exceeds_contract_balance` — the date cannot be what failed. |

**Reports** bucket collected money by **`paid_at`**, not `created_at`, so a
backfilled year lands in the periods it was actually paid in; `source` is a
filter, not an exclusion. A test asserts it with a contract backfilled across a
year.

**Phase 20 audit actions:** `contract.backfill` (entity `contract`), beside the
ordinary `payment.record` rows the settlement writes.

**Phase 20 migration:** `000021_payment_source` — `payments.source TEXT NOT NULL
DEFAULT 'manual' CHECK (source IN ('manual','import','backfill'))`, backfilled
`'import'` for every row with an `import_batch_id`, plus an index on
`(org_id, source)`. No schedules-side column: `last_payment_source` is a join.

**Deviations (Phase 19/20 backend, 20 Sep 2026)** — implemented as written above except:

| Endpoint | Deviation | Why |
|---|---|---|
| `PATCH /org/members/me`, `PATCH /org/members/{id}` | The response is `{member, user}`, not `{member}` alone. | Phase 13's locale-only call answered `{user}`, and a client written against it keeps working; `member` is the row the staff list redraws from. A `{locale}`-only body still takes the Phase 13 path exactly, down to the `user.locale_update` audit action. |
| `PATCH /renters/{user_id}` | The response is the full `GET /renters/{user_id}` body (`{renter, profile, link_requests, contracts}`). | The tenant app redraws the renter card from the answer rather than re-fetching it, and the card needs those blocks. |
| `PATCH /admin/users/{id}` on a **renter** | The `name_corrected` SMS is sent **as the org whose rent book carries the name** (the renter's first live tenancy, else any org that knows them), and is **not sent at all** when no org does. | `notification_log.org_id` is NOT NULL — the platform has no SMS budget of its own. The org whose records hold the name is also the one the renter would ring if the correction were wrong. A renter with no tenancy has the name on no document yet. |
| `PATCH /admin/users/{id}` on a **renter** | That org's **`name_corrected` toggle, SMS language and template override** all apply: the message is not sent at all when the org has switched the kind off, and carries the org's own wording when it has written one. | The org's credits pay for it. Borrowing the budget while ignoring the configuration would text in the wrong language, from an org that had turned the kind off, over a template the landlord had replaced. |
| `GET /admin/users/{id}` | `nida_masked` is present only for a renter (absent, not null, otherwise), and the audit block is paged by **`?audit_cursor=`** on the same route. | One fetch draws the whole page; a second endpoint for the Activity tab would have been a second place for the cursor encoding to drift. |
| `GET /admin/audit-log` | Gains an exact **`?action=`** filter beside the existing `?q=` substring. | "Show me every NIDA reveal" must not also match `admin.user_view` because both contain the word "user". |
| `POST /contracts/{id}/backfill` | The response also carries **`schedules:[…]`** — every row in range, settled and skipped, in the `schedule` shape. | The sheet that made the call redraws the rent book from the answer. `paid_at` accepts the literal `"due_date"` (the default) as well as a date. A `partial` row is settled for its **remainder**; only `paid` and `waived` rows are skipped. |
| `POST /contracts/{id}/backfill` | It does **not** go through the `POST /payments` allocator. | The allocator decides *where* a sum lands and rolls an overpayment forward; a backfill has no such question (one payment per period, for that period's remainder) and going through it would queue one `thank_you` per period, which is the N texts this endpoint exists to avoid. The per-payment `payment.record` audit rows are written all the same. |
| `GET /payments` | The CSV is `?format=csv` on the same route (columns `paid_at, renter_name, property, unit, amount, method, reference, source, status, note`), not a separate export endpoint. There was no payments export before this phase. | The ledger and its export take the same filters; a second route would have been a second place for `?source=` to be forgotten. An export ignores both the cursor and the screen's 200-row page limit, and is capped at **10 000 rows** — the same bound the expenses export carries. |
| `POST /contracts` | The 10-year backstop is a **new bound in both directions**: the route previously had none at all, and now also refuses a start date more than a year ahead, matching `POST /units/{code}/link`. | An unbounded `start_date` on the manual path was an accident, not a feature; the phase that opens the past deliberately is the phase to close the rest. |
| `renters` import | Gains an optional **`start_date`** column (the real move-in date, up to 10 years back, default today), echoed in the preview's `resolved`. | PLAN2 §20.3 asks the import to accept a past start date, and the sheet had no column to carry one. |
| `POST /imports/preview` (`kind=payments`) | The Backfill hint replaces `exceeds_contract_balance` **only when the row both fails allocation and predates the first schedule period**. | A payment dated before the book that still has an unpaid period to land on is allocated, exactly as in Phase 16 — the hint explains a refusal, it does not invent one. |
| `PUT /org/notification-settings` | `kinds` gains **`name_corrected`** and **`backfill_done`** toggles, both defaulting to on. | PLAN2 asks for both kinds to be disable-able. They are the only two whose stored form is nullable, so a settings blob written before this phase resolves to "on" rather than silently switching both messages off. |
| `POST /renters/{user_id}/nida/reveal` | The 200 carries `Cache-Control: no-store` and `Pragma: no-cache`. | The one response in the product holding a national ID number must not be storable by a browser, a proxy or a service worker. |

## Part 2 — Phase 21: renames after signing, arrears after move-out

### 21.1 Renter rename after signing
See `PATCH /renters/{user_id}` in 19.3 (amended): `reason` required once signed with this org, 409 `renter_signed_elsewhere` when signed with another.

### 21.2 Arrears after move-out

| Route | Contract |
|---|---|
| `POST /payments`, `POST /me/proofs`, `POST /proofs/{id}/accept` | Now accept contracts in `ended` and `terminated` as well as `active`/`expiring`; 409 `contract_not_active` only for unsigned contracts. Money lands on rows still owing (`pending`/`partial`/`overdue`); an amount above what is owed → the existing exceeds-balance refusal (there is no future to roll into). `POST /contracts/{id}/backfill` stays running-only. |
| `GET /arrears?cursor=&limit=&property_id=` | Org audience, owner + manager. Closed contracts (`ended`/`terminated`) with at least one row owing, ordered by `closed_on` desc (termination effective date, else end date), cursor-paged. → `{items:[{contract_id, contract_status, closed_on, renter:{id, full_name, phone}, unit_id, unit_name, property_id, property_name, outstanding, periods, oldest_due, last_paid_at}], next_cursor, total:{outstanding, contracts}}`. |
| `POST /contracts/{id}/write-off` | **Owner only.** `{reason}` (required, ≤200). Every `pending`/`partial`/`overdue` row → `written_off` (paid money on a partial row stays). 409 `contract_not_closed` on a running contract, 409 `nothing_owing`. → `{periods, amount, schedules}`. Audit `contract.write_off` `{periods, amount, reason}`. |
| `POST /contracts/{id}/write-off/undo` | **Owner only.** Rows back to `paid`/`overdue`/`partial`/`pending`, recomputed from their own money and dates. 409 `nothing_written_off`. → `{periods, amount, schedules}`. Audit `contract.write_off_undo`. |

Schedule rows gain `write_off_reason` (only on `written_off`). Reports: `written_off` counts in `expected`, never in `outstanding`/`overdue`; a written-off row takes no payment.

## Part 2 — Phase 22: contract templates per unit

### 22.1 Template assignment
Resolution, first hit wins: template named at approval/creation → unit's → property's → org default (soft-deleted assignments skipped). The contract-create audit row carries `template_source` (`explicit|unit|property|default`).

| Route | Contract |
|---|---|
| `GET /units/{id}/template` | Org audience. → `{template: {id, name, source} \| null}` — what a tenancy of this unit would be written on. |
| `POST /units/bulk-template` | Org audience. `{unit_ids: uuid[1..500], template_id: uuid \| null}` (key required; `null` clears). Ids outside the org match nothing. → `{updated, contract_template_id}`. Audit `unit.template_set` per unit. 400 on a template not in the org. |
| `PUT /properties/{id}/template` | Org audience. `{template_id: uuid \| null}` → `{contract_template_id}`. Audit `property.template_set` (before/after). |
| `POST /link-requests/{id}/approve` | Optional body `{template_id}` overrides the unit's resolved template for this one contract. 409 `template_not_found` when it is not a live template of the org. |
| `GET /contract-templates` | Items gain `usage: {units, properties}`. |
| `DELETE /contract-templates/{id}` | 409 `template_in_use` `{units, properties}` while assigned. |

Unit and property responses gain `contract_template_id` (own assignment, null = inherit).

### 22.2 Contract policies
`contract_templates.policy` and `contracts.policy` (JSONB, migration 000024). Shape:
`{move_out_proration: full_month|pro_rata, early_exit_prepaid: refund|forfeit|landlord_decides, deposit_mode: none|fixed|months, deposit_amount, deposit_months (1..24), deductions_may_exceed_deposit, tenant_notice_days (0..365), eviction_notice_days (0..365)}`.

- `POST /contract-templates`, `PATCH /contract-templates/{id}`: optional `policy` — absent leaves it, `null` clears, an object is validated (400 with `policy.<field>` errors; unknown members refused) and stored normalised (fields the deposit mode does not use are zeroed). Template responses carry `policy` (null when none). Patch audit carries policy before/after.
- Contract creation copies the template's policy onto the contract with the deposit **resolved to an amount** (`months` × the unit price scaled to 30 days). `contract.policy` on every contract response (null before Phase 22). The snapshot hash appends the policy's canonical JSON **only when present**, so every earlier hash still verifies.
- New document variables `{{deposit}}` (amount, or "none"/"hakuna"), `{{tenant_notice_days}}`, `{{eviction_notice_days}}` — blank on a template without a policy.

### 22.3 Unsigned contracts on reworded templates
Migration 000025: `contract_templates.content_updated_at` (moves only when `body_html`, `body_html_sw` or `policy` changes — not on a rename or default flip) and `contracts.supersedes_contract_id`.

| Route | Contract |
|---|---|
| `GET /contracts/{id}` and every contract reload | `template_changed: bool` — `pending_signature`, **nobody has signed yet**, and written before its template's content last changed. `supersedes_contract_id` on every contract. |
| `PATCH /contract-templates/{id}` | Response gains `stale_pending`: unsigned, unsigned-by-anyone contracts still on the old content. |
| `POST /contracts/{id}/reissue` | Org audience. Optional `{reason (≤200, default "contract wording updated"), template_id}`. Only `pending_signature` with **no signature** (409 `not_pending_signature`, 409 `already_signed`). In one transaction: the old contract is terminated with reason "Reissued: …", a new one is written from the same unit, renter, period, term, start date, due day, language and application — on `template_id`, else its own template (now reworded), else the unit's resolved template if that one was deleted — with `supersedes_contract_id` set. One SMS (`contract_ready` for the new one). Audit `contract.reissue` `{reason, new_contract_id}` then the new `contract.create`. → 201 `{contract}`. |
| `POST /contract-templates/{id}/reissue-pending` | Org audience. Optional `{reason}`. Reissues every stale contract of the template, all or none. → `{reissued}`. |

### 22.4 Amendments and renewals (Phase 17 supersession)
Migration 000026: `contracts.amendment_effective_date`, `amendment_reason`, `superseded_by_contract_id`; the one-live-contract-per-unit index now excludes amendments; `contracts_one_open_amendment` allows one unsigned amendment per contract.

`POST /contracts/{id}/amend` — org audience, `active`/`expiring` only (409 `contract_not_active`). Body: `effective_date` (required), `reason` (required, ≤200), optional `rent_amount`, `rent_period_days`, `payment_period_id`, `due_day`, `term_days`, `template_id`, `language`. Anything not given is carried from the running contract (rent is the contract's, **not** the unit's current price). `effective_date` must be the `period_start` of one of the contract's periods on or after today, or its `end_date` (a **renewal**) — else 422 `effective_not_period_start` `{allowed: [...]}`. Term defaults to the old end date (or the old term for a renewal). → 201 `{contract}`: a new `pending_signature` contract with `supersedes_contract_id`, `amendment_effective_date`, `amendment_reason`; `contract_ready` SMS. 409 `amendment_pending` while one is open. Audit `contract.amend` (before/after terms).

Activation of an amendment (`POST /contracts/{amendment}/activate`, unchanged call): in the same transaction the old contract's periods starting on/after the effective date are **waived**, live payment allocations on them are **moved** to the amendment's periods in order (same payments; leftover beyond the new contract's rent stays on the old row), and the old contract gets `superseded_by_contract_id` and `termination_effective_date = effective − 1`; it ends at once if that day has passed, else the lifecycle job ends it. 409 `amendment_stale` if the old contract stopped running meanwhile. Audit `contract.supersede` `{superseded_by, effective_date, waived, carried}`. Reversing a carried payment unwinds it from the amendment's periods. Withdrawing an amendment = `POST /contracts/{amendment}/terminate` (as for any unsigned contract).

Contract responses gain `amendment_effective_date`, `amendment_reason`, `superseded_by_contract_id`, `termination_effective_date`.

### 22.5 Settle-up on termination, deposits
Migration 000027: `rent_refunds` (+ `rent_refund_items` per payment), `deposit_entries` (`received|deduction|refund|applied_to_rent`), payment method `deposit`, `contracts.settlement` (JSONB).

**Termination now settles by the contract's policy** (`POST /contracts/{id}/terminate`, running contracts; unsigned ones unchanged). Body gains optional `prepaid_action` (`refund|forfeit` — required, else 422 `prepaid_choice_required`, when the policy says `landlord_decides` and rent is prepaid), `refund_method` (required when anything is refunded, else 400), `refund_reference`. Effective date = last day lived (unchanged). Applied in the termination's transaction:
- the period the end falls inside is re-priced **pro rata by day** when the policy says so (`full_month` or no policy: charged in full);
- rent paid for time after the end (whole later periods, plus any excess on the re-priced period) is **refunded** (allocations taken back newest first, a `rent_refunds` row with per-payment items) or **kept** (`forfeit`; no policy = kept, the previous behaviour); later periods become `waived` either way;
- the settlement is stored on the contract (`contract.settlement`) and audited `contract.settle`.

`GET /contracts/{id}/settlement?effective_date=&prepaid_action=` — the same computation without writing: `{settlement: {effective_date, proration, straddle: {schedule_id, amount, charged, days_lived, period_days} | null, arrears, prepaid, prepaid_action, refund, deposit_required, deposit_held, net}, policy, needs_choice}`. `net` > 0: the renter still owes; < 0: the landlord owes (refund + deposit held).

**Deposit ledger** — `GET /contracts/{id}/deposit` → `{deposit: {required, received, held, owed_beyond_deposit, entries[], rent_refunds[]}}`. `POST /contracts/{id}/deposit {kind, amount, method?, reference?, reason?, occurred_at?}` (signed contracts only, 409 `contract_not_signed`): `received`/`refund` need a method; `deduction` needs a reason and may exceed what is held only if the policy allows (`deductions_may_exceed_deposit`; the excess shows as `owed_beyond_deposit`), else 422 `exceeds_deposit_held {held}`; `refund`/`applied_to_rent` ≤ held. `applied_to_rent` becomes an ordinary rent payment (method `deposit`) through the allocator. Audit `deposit.record`.

**Reports**: cash figures (summary `collected`, collections, revenue daily and by property) subtract rent refunds in the period they were paid out. **Reversal**: 409 `payment_refunded` for a payment part of which was refunded; 409 `deposit_payment` for money applied from the deposit.

### 22.5 (rest) Period relief, notice to leave, holdover, eviction
Migration 000028: schedule `original_amount`, `adjustment_kind|reason|at|by`; contract `notice_given_at`, `notice_leave_on`, `notice_reason`, `moved_out_confirmed_at`; table `eviction_cases`; SMS kinds `notice_received`, `eviction_demand`, `eviction_notice`, `eviction_withdrawn` (SW/EN, credits apply; `{{date}}` platform-only).

| Route | Contract |
|---|---|
| `POST /schedules/{id}/adjust` | Org. `{kind: waive\|discount, discount?, reason}` on an unsettled, unadjusted period. Waive → `waived` (money paid stays); discount (1 … amount−1) → amount lowered, status recomputed. 409 `not_adjustable`. Audit `schedule.adjust`. Schedule rows gain `original_amount`, `adjustment_kind`, `adjustment_reason`. |
| `POST /schedules/{id}/adjust/undo` | Org. Restores the amount and recomputes status. 409 `not_adjusted`. |
| `POST /me/contracts/{id}/notice`, `POST /contracts/{id}/notice` | Renter (own contract) or org (told in person). `{leave_on, reason?}` on a running contract; `leave_on ≥ today + policy.tenant_notice_days` (422 `notice_too_short {earliest}`) and before `end_date` (422 `after_end_date`). SMS `notice_received`. Audit `contract.notice`. Contract gains `notice_given_at`, `notice_leave_on`, `notice_reason`. |
| `DELETE /me/contracts/{id}/notice`, `DELETE /contracts/{id}/notice` | Withdraws it; 409 `no_notice`. |
| `GET /holdovers` | Org. Tenancies that **ran out** (status `ended`) in the last 90 days, not renewed, not confirmed moved out → `{items:[{contract_id, end_date, renter, unit_id, unit_name, property_name}]}`. |
| `POST /contracts/{id}/moved-out` | Org. Confirms an ended/terminated tenancy is empty (`moved_out_confirmed_at`); 409 `not_closed`. |
| `POST /contracts/{id}/amend` | Now also renews an **ended** contract when `effective_date` = its end date (holdover renewal); refused if the unit has been let since. On activation the ended contract is linked (`superseded_by_contract_id`), nothing waived. |
| `POST /contracts/{id}/eviction` | Org. Opens a case at **demand**: running contract with arrears past the grace days (409 `no_arrears`), one open case per contract (409 `eviction_open`). `{pay_by?}` default today+7. `notice_days` = policy `eviction_notice_days` (else 30). SMS `eviction_demand` (amount, pay-by). → 201 `{eviction}`. |
| `POST /evictions/{id}/notice` | Demand → **notice**: `vacate_by` = today + notice days; SMS `eviction_notice`. 409 `not_at_demand`. |
| `POST /evictions/{id}/withdraw` | `{reason}` → **withdrawn**; SMS `eviction_withdrawn`. 409 `case_closed`. Terminating the contract closes an open case as **vacated**. |
| `GET /contracts/{id}/eviction`, `GET /evictions` | The open case (with `arrears_now`) and history; the org's open cases. |
| `GET /evictions/{id}/letter?kind=demand\|notice&lang=sw\|en` | `{html, kind, lang, total}` — printable letter with today's arrears statement (periods past due, rent, paid, owing, total). Platform wording, not org-editable. 409 `no_notice_yet` for a notice letter before the notice. |

Audit actions `schedule.adjust`, `schedule.adjust_undo`, `contract.notice`, `contract.notice_withdraw`, `contract.moved_out`, `contract.eviction`.

## Part 2 — Phase 23: pagination

`GET /reports/payment-status` and `GET /reports/upcoming` now page the finished list: `?limit=` (1–500, default 100) and `?cursor=` (opaque; the previous page's `next_cursor`). Payment-status JSON gains `next_cursor`, `total` (rows after the status filter) and `counts` (per status, over every running tenancy); the CSV export is still the whole list. Upcoming keeps `total_due` and `count` over the whole window and gains `next_cursor`. The per-contract helper queries behind both now read running tenancies only.

## Part 2 — Phase 24: payment corrections and landlord notices

Migration 000029: `payments.idempotency_key` (unique per org), `payments.corrects_payment_id`; `org_inbox` + `org_inbox_reads`; SMS kinds `payment_reversed`, `payment_corrected` (always sent; `{{reason}}`, `{{date}}`, `{{next_amount}}` platform-only).

| Route | Contract |
|---|---|
| `POST /payments` | Header `Idempotency-Key` (≤80): a repeat with the same key → **200** `{payment, replayed: true}`, nothing written. **409 `possible_duplicate`** `{matches: [{id, amount, paid_at, method, reference}]}` when a live payment on the same contract has the same amount within ±3 days, or any payment in the org has the same reference (case-insensitive) within 90 days; body `confirm_duplicate: true` records it anyway and writes an inbox notice. |
| `POST /proofs/{id}/accept` | Same duplicate check against the proof's amount/date/reference; `confirm_duplicate: true` to accept anyway. |
| `POST /payments/{id}/correct` | Org. `{reason (required), amount?, contract_id?, schedule_id?, paid_at?, method?, reference?, allow_overpay_rollover?}` — reverses the payment and records the corrected one in **one transaction** (anything not sent is copied from the original; allocator refusals as for `POST /payments`). New payment carries `corrects_payment_id`. Same refusals as reverse (`already_reversed`, `deposit_payment`, `payment_refunded`). → 201 `{payment, reversed}`. Audit `payment.correct`. SMS `payment_corrected` to the original renter. |
| `POST /payments/{id}/reverse` | Unchanged contract; now also texts the renter (`payment_reversed`) and writes an inbox notice. |
| `GET /inbox?cursor=&limit=` | Org. The landlord's notices, newest first, each with `read` for the caller: `{id, kind, title, body, entity_type, entity_id, link, read, created_at}`. Kinds so far: `payment_reversed`, `payment_corrected`, `payment_duplicate_confirmed`, `proof_submitted`, `notice_given`. |
| `GET /inbox/unread` | `{unread}` for the caller. |
| `POST /inbox/read` | `{ids: [...]}` or `{all: true}` → `{unread}`. Read state is per user. |

## Part 2 — Phase 25: landlord-assisted onboarding screens

No migration. One additive change to the Phase 18 contract:

| Route | Contract |
|---|---|
| `POST /assist`, `POST /assist/{id}/code` | Both responses also carry `link` (the renter's `{ENDUSER_URL}/u/{unit_code}?assist={session_id}`) and `link_qr` — a `data:image/png;base64,…` QR of that link (256 px, `""` if encoding failed). The QR travels inline, not through the `qrcodes` bucket, because it lives only as long as the session. `GET /assist` and `GET /assist/{id}` are unchanged and still never carry a code, link or QR. |

Frontends: tenant `/renters/assist` (`?unit=`, `?renter=`, `?id=`) and the contract page's **Witness signing** sheet; enduser `/u/{unit_code}?assist={id}` keeps the id in `sessionStorage` (checked against `GET /public/assist/{id}`) until the link request is sent, and register/login/sign skip `POST /auth/otp/send` / `sign-otp` in that mode.
## Part 2 — Phase 26: undo a whole backfill, bulk backfill by CSV

Migration 000030: `backfill_batches` (`org_id, contract_id, mode paid|waived, until, periods, amount, import_batch_id?, created_by_user_id, created_at, undone_at, undone_by_user_id, undo_reason`), `payments.backfill_batch_id` (the money a `paid` batch wrote), `payment_schedules.backfill_batch_id` (the rows a `waived` batch closed); import kind `backfill`. Audience **org**, owner + manager, like the backfill itself.

| Route | Contract |
|---|---|
| `POST /contracts/{id}/backfill` | Unchanged, except that a call which settles anything writes **one batch** and the response gains **`backfill_id`** (null when nothing was settled — no batch is kept for a call that closed nothing). Each payment's `payment.record` audit and the `contract.backfill` audit carry `backfill_id`. |
| `GET /contracts/{id}/backfills` | → `200 {items:[backfill…]}`, newest first, undone ones included. Another org's contract → 404. |
| `POST /backfills/{id}/undo` | `{reason}` (required, ≤200 — it becomes every reversed payment's reason, prefixed `Backfill undone: `) → `200 {backfill, payments_reversed, periods_reopened}`. In **one transaction**: every live payment of the batch is reversed through the ordinary reversal path (`payment.reverse` audit per payment, schedules debited and recomputed), and every row the batch waived goes back from `waived` to `paid\|overdue\|partial\|pending`, recomputed from `paid_amount`, due date and the org's grace days. No time window. Refusals (409, nothing written): **`touched_since`** — a live payment that is not the batch's own, recorded after the batch, is allocated to any period the batch settled or waived; **`already_undone`**; **`payment_refunded`** — a settle-up refunded part of the batch's money. Unknown id or another org's → 404. Audited **`backfill.undo`** (entity `backfill_batch`, before: contract, mode, until, periods, amount; after: reason and the counts). **No SMS** and no inbox notice. |

```jsonc
// backfill
{ "id": "…", "contract_id": "…", "mode": "paid",        // paid | waived
  "until": "2026-08-31", "periods": 12, "amount": 3000000, // money moved (paid) or forgiven (waived)
  "import_batch_id": null,                                // the CSV import the line came from
  "created_by": {"user_id": "…", "name": "Joseph Chuchu"},
  "created_at": "2026-09-27T09:12:00Z",
  "undone_at": null, "undone_by": null, "undo_reason": null,
  "touched": false,                                       // other money has reached its periods since
  "can_undo": true }                                      // not undone and not touched
```

**Import kind `backfill`** — the Phase 16 pipeline unchanged (preview → commit → 24 h undo, 2 MiB, 5 000 rows, 20 previews an hour). Columns `renter_phone, unit_code, until, mode, paid_at?, method?, reference?, note?`; template at `GET /imports/templates/backfill.csv`.

| Rule | Behaviour |
|---|---|
| Resolution | `renter_phone` must be a renter this org knows (another org's renter and nobody give the same error); `unit_code` (the QR code, case-insensitive) must be this org's unit; the tenancy is the renter's **running** (`active\|expiring`) contract on that unit. |
| Cell rules | As `POST /contracts/{id}/backfill`: `until` a `YYYY-MM-DD` not in the future and not before the contract's start; `mode` `paid\|waived`; `note` required for `waived`; `method` defaults to `cash`; `paid_at` blank or `due_date` = each period's own due date, or one date. |
| Preview | Each ok row's `resolved` carries `renter_name, unit, property, contract_id, mode, until, periods, amount` — "N periods · TZS X", counted from the rent book as it is. A line that would settle nothing, and a second line for the same tenancy, are row errors. |
| Commit | **Any row error blocks the commit — `skip_errors` is refused** with `409 batch_has_errors`. Each line runs the same core as the button, so each line is one batch (`import_batch_id` set), one `contract.backfill` audit row and at most one `backfill_done` SMS. `created.backfills` counts them; each row is stamped `entity_type:"backfill_batch"`. |
| Undo | `POST /imports/{id}/undo` undoes each of the import's batches exactly like `POST /backfills/{id}/undo` (reason `import undone`). A batch refused as touched/refunded is **kept and not counted**, like anything else an import undo leaves; it can still be undone from the contract page later. `undone.backfills` counts the rest. |

`created` and `undone` gain a **`backfills`** count for every kind (0 elsewhere).
## Part 2 — Phase 29: backfill before the contract's start date

Migration 000033: `payment_schedules.created_by_backfill_id` (periods a backfill created), `backfill_batches.from_date`, `backfill_batches.created_periods`.

| Route | Contract |
|---|---|
| `POST /contracts/{id}/backfill` | Body gains **`from?: "YYYY-MM-DD"`** — the real move-in; must be before the contract's `start_date` and at most 3650 days before it (else 422 naming `from`) — **`period_amount?: int`** — rent per payment period for the created periods, whole TZS, `0 < x < 10^12`, default the contract's per-period rent; without `from` → 400 naming `period_amount` — and **`dry_run?: bool`**. With `from`: the periods from `from` up to the day before `start_date` are generated at the contract's payment-period cadence and due day (last one truncated and prorated), status `overdue` past due + grace else `pending`, stamped `created_by_backfill_id`; then every unsettled row due ≤ `until` is settled as before. `until` must be ≥ `from` (422). Without `from`, `until` must be ≥ the first existing period (the contract's `start_date` unless an earlier backfill created history) (422). `from` while the contract already has periods before `start_date` → **409 `history_exists`**. Response gains **`created`** (periods created). A call that created periods keeps its batch even if it settled none. `dry_run: true` runs the whole call and rolls it back: same response with `dry_run: true`, `backfill_id: null`, nothing written, no SMS. `contract.backfill` audit gains `from`, `created`. |
| `GET /contracts/{id}/backfills` | Each item gains `from` (date or null) and `created_periods`. |
| `POST /backfills/{id}/undo` | Also soft-deletes the periods the batch created, after reversing their money; response gains `periods_removed`. `touched_since` also counts a later non-batch payment allocated to a created period. |
| `GET /contracts/{id}/document` | `schedule` leaves out created periods — history, not signed terms. |
| CSV `backfill` kind | Optional columns **`from`**, **`period_amount`** (same rules; `period_amount` without `from` is a row error; `from` on a contract with history is a row error). Preview `resolved` gains `from` and `created`; `periods`/`amount` include the created periods. |

## Part 2 — Phase 28: projections, break-even and ROI

Migration 000032: `properties.purchase_price` (BIGINT > 0), `purchase_date` (DATE), `current_value` (BIGINT > 0) — all nullable; `expense_categories.is_capital` (BOOLEAN, default false); table `projection_scenarios` (per org, unique name case-insensitively, at most 50).
Migration 000035 (30 Sep 2026): `projection_scenarios.monthly_expenses` (BIGINT ≥ 0, nullable), `from_purchase`, `include_future_expenses` (BOOLEAN, default false).
Migration 000034 (rework, 27 Sep 2026): `projection_scenarios.basis` (`contracts` | `selected` | `best_case`, default `contracts`) and `unit_ids` (UUID[]); `occupancy_pct` dropped (saved scenarios that set it became `best_case`).

| Route | Contract |
|---|---|
| `PATCH /properties/{id}` | Also takes `purchase_price`, `purchase_date` (YYYY-MM-DD, 1900…today), `current_value` — each whole TZS 1 … 999,999,999,999 or `null` to clear; absent = unchanged. 400 field errors. Audited on `property.update`. `GET /properties`, `GET /properties/{id}` return the three fields (null when unset). |
| `POST /org/expense-categories`, `PATCH /org/expense-categories/{id}` | Also take `is_capital` (bool); the category shape gains `is_capital`. Capital spend counts toward what a property has cost, not as a monthly running cost. Audited on `expense_category.update`. |
| `POST /reports/projection` | Org (any role). Body, all optional: `{property_id, horizon_months (1–120, default 24), basis ("contracts" default \| "selected" \| "best_case"), unit_ids ([uuid], ≤1000, used only by "selected"), rent_change_pct (−100…500, default 0), collection_rate_pct (0–100 or null = trailing), expense_change_pct (−100…500, default 0; on the trailing average only), monthly_expenses (whole TZS ≥ 0, or null = the trailing average), from_purchase (bool, default false), include_future_expenses (bool, default false)}`. Unknown fields (incl. the old `occupancy_pct`) / out-of-range / unknown basis / malformed ids → 400 field errors; a property not in the org → 404. Unit ids outside the org or the property are ignored. Writes nothing. → 200 below. |
| `GET /reports/projection/scenarios` | Org. `{items: [scenario]}` sorted by name. `scenario = {id, name, horizon_months, basis, unit_ids, rent_change_pct, collection_rate_pct, expense_change_pct, monthly_expenses, from_purchase, include_future_expenses, created_at}` (collection null = trailing). |
| `POST /reports/projection/scenarios` | Org. `{name (1–60), …the projection parameters}` → 201 `{scenario}`. 409 `scenario_exists` (same name, any case); 422 `too_many_scenarios` past 50. Audit `projection_scenario.create`. |
| `DELETE /reports/projection/scenarios/{id}` | Org. 204; 404 for another org's or a missing one. Audit `projection_scenario.delete`. |

**Response** of `POST /reports/projection`:

```
{ scope: "property" | "portfolio", property: {id, name} | null, start_month: "YYYY-MM",
  baseline: { history_from, history_to (exclusive), history_months, collected, expected,
              collection_rate_pct | null, running_expenses, capital_expenses,
              running_expenses_monthly, categories: [{id, name, monthly}], net,
              units_total, units_let, units_open, units_assumed, assumed_rent_monthly },
  applied:  { horizon_months, basis, unit_ids, rent_change_pct, collection_rate_pct,
              collection_rate_source, expense_change_pct,
              monthly_expenses, monthly_expenses_source, from_purchase, include_future_expenses },      -- source: scenario | trailing | default
  months:   [{ month: "YYYY-MM", income, running_expenses, net,
               cumulative_income, cumulative_spent, cumulative }],
  totals:   { income, running_expenses, net },
  investment: { purchase_price | null, capital_spend, running_spend, spent_to_date, income_to_date,
                cash_to_date, current_value | null, break_even_month | null, break_even_status,
                trailing_annual_net, trailing_annual_spend, projected_annual_net, projected_annual_expenses,
                roi_spend, roi_trailing_pct | null, roi_projected_pct | null, yield_pct | null, payback_years | null },
  units:    [{ id, name, property_id, property_name, status, monthly_rent, let_until | null,
               lettable, assumed }],
  properties: [{ id, name, has_purchase_price, projected_annual_net, projected_annual_expenses,
                 spent_to_date, roi_projected_pct, yield_pct, payback_years, break_even_month,
                 break_even_status }] }
```

**The model** (all arithmetic in `internal/report/projection.go`; money is whole TZS, percentages to one decimal):

- The forecast starts on the 1st of the current month (Dar es Salaam wall clock). The **trailing window** is the twelve whole months before it; a property younger than that is averaged over the months it has been in the book (`history_months`, from its creation or its first recorded activity).
- **Cash** is the revenue report's: non-reversed payments by `paid_at` less rent refunds by `refunded_at`; **expected** is non-waived schedules by `due_date`; **expenses** are recorded (not voided) by `incurred_on`, split by the category's `is_capital`.
- Trailing **collection rate** = collected ÷ expected, capped at 100%; with no history it defaults to 100% (`source: default`).
- **Basis** — which units count as let beyond their signed contracts. There is no occupancy percentage:
  - `contracts`: only rent scheduled on `active`/`expiring` tenancies; a contract that ends is not renewed, vacant units earn nothing.
  - `selected`: the same, plus each unit in `unit_ids` let at its own price from the month no contract covers it (any status — a landlord may pick a unit under maintenance).
  - `best_case`: every lettable unit (`vacant`/`occupied`) let at its own price whenever no contract covers it — contracts assumed renewed.
- A month's **income** = (rent scheduled that month + for each unit the basis counts as let and no contract covers, its price × (1 + `rent_change_pct`)) × collection rate. A unit's price = its current price plan, else the latest tenancy's rent, normalised to 30 days; a unit whose tenancy ends mid-month is free from the month after. Signed contracts keep their rent.
- A month's **running expenses** = `monthly_expenses` when given (the landlord's estimate, used as is); otherwise trailing non-capital expenses ÷ `history_months` × (1 + `expense_change_pct`). For the portfolio an estimate is shared across properties by their trailing running costs (else unit counts, else equally), so the parts still add up.
- The forecast starts **next month**: the current month so far is history, counted in the trailing window and the to-date figures.
- **Returns are measured against spend** — what the rent book records. **Spend** = every running and capital expense since the purchase month (all history without a `purchase_date`) + `purchase_price` when entered. `cash_to_date` = income to date − spend to date. `cumulative_income` / `cumulative_spent` carry both forward month by month; `cumulative` is their difference.
- **Break-even** is the month cumulative income covers cumulative spend: `reached` (already, at the month it last came back above zero), `projected`, `beyond_horizon` (still profitable), `not_profitable` (projected annual net ≤ 0), `no_costs` (income covers every cost from the start — nothing to break even on).
- **Total expenditure**: by default every expense ever logged counts (and all income); `from_purchase: true` drops recorded months before each property's purchase date. `roi_spend` = `spent_to_date`, plus the first projected year's expenses when `include_future_expenses: true`.
- **ROI** = a year's net ÷ `roi_spend`. `projected` = the first twelve projected months' net (annualised when the horizon is shorter); `trailing` = the trailing net annualised. Null when nothing has been spent. **Yield** = projected annual net ÷ `current_value`. **Payback** = years at the projected annual net until income covers the spend to date: `0` once it has, null when nothing was spent or the net is ≤ 0.
- **Portfolio**: months, totals and units are the sum of the properties; the return figures are computed on the sums (a purchase price, where entered, adds to spend); yield covers properties with a current value.
## Part 2 — Phase 27: SMS credits bought with mobile money (Snippe), platform SMS stock

Migration 000031: `sms_credit_packages`, `sms_credit_orders`, `snippe_webhook_events`, `platform_sms_purchases`; ledger reason `purchase`. Config: `SNIPPE_API_KEY`, `SNIPPE_WEBHOOK_SECRET`, `SNIPPE_BASE_URL` (default `https://api.snippe.sh`), `SNIPPE_WEBHOOK_URL` (default `{PUBLIC_BASE_URL|APP_BASE_URL}/api/v1/webhooks/snippe`), `SMS_STOCK_BUFFER` (default 1000). **Purchases are on only when both the key and the webhook secret are set**; otherwise the purchase route (and the webhook) answer **503 `purchases_disabled`** and everything else works.

Order statuses: `pending` → `completed` (credited) | `failed` (Snippe failed/voided, or refused the push) | `expired` (Snippe expired it, or still pending after 4 h) | `mismatch` (Snippe reported a completed payment of another amount/currency — never credited automatically). A completion that arrives after a local `expired`/`failed` still credits (the money was taken).

| Route | Contract |
|---|---|
| `GET /org/sms-credits/packages` | Org. → `{enabled, default_phone, items:[{id, name, credits, price, active, sort_order, …}]}` — active packages, cheapest first by `sort_order`, `price`. `default_phone` is the caller's account phone. |
| `POST /org/sms-credits/orders` | Org (owner or manager). `{package_id, phone?}` — `phone` any Tanzanian mobile (07…, 2557…, +2557…), default the account phone; stored normalised on the order. Writes the order (credits, price and name copied from the package; `order_code` `SMS-XXXXXXXXXX` ≤30 chars) then calls Snippe `POST /v1/payments` (`amount`, `currency: TZS`, `phone` as `2557…`, `payment_type: mobile`, `metadata.order_code`, `webhook_url`) with `Idempotency-Key: order_code`. → **201** `{order}` (pending, with `reference`). 400 field errors (`package_id` not on sale, `phone`); 429 `too_many_orders` after 5 orders in 10 min; 502 `payment_request_refused` (Snippe 4xx; the order is `failed` with the reason); 502 `payment_provider_unreachable` (no answer / 5xx; the order stays pending — a webhook naming its code still credits it); 503 `purchases_disabled`. Audit `sms_credits.order`. |
| `GET /org/sms-credits/orders` | Org. → `{items:[order], enabled}` — newest 50. |
| `GET /org/sms-credits/orders/{id}` | Org; another org's id → 404. → `{order}`. The waiting screen polls it; a pending order at least 20 s old and not checked in the last 20 s is checked with Snippe (`GET /v1/payments/{reference}`) before answering. |
| `POST /webhooks/snippe` | **Public** (Snippe holds no session). Headers `X-Webhook-Timestamp` (unix s), `X-Webhook-Signature` = hex HMAC-SHA256 of `{timestamp}.{raw body}` with `SNIPPE_WEBHOOK_SECRET` (constant-time; a `sha256=` prefix is tolerated). Timestamp more than 5 min off → **401 `stale_webhook`**; bad signature → **401 `invalid_signature`**; nothing recorded. Verified: the event id is inserted into `snippe_webhook_events` in the same transaction as its effect — a repeat → 200 `{received, outcome: "duplicate"}`. The order is found by `data.reference`, else `data.metadata.order_code`. `payment.completed` with `data.amount.value` = order amount and currency TZS → order `completed` and, in the same transaction, `notify.Add(reason purchase)` + release of every `held_no_credit` message (queued after commit); another amount → `mismatch`. `payment.failed`/`payment.voided` → `failed`; `payment.expired` → `expired`. → 200 `{received: true, outcome}` with outcome `completed|already_completed|amount_mismatch|failed|expired|pending|ignored|unknown_order|duplicate` (unknown orders and mismatches are recorded and answered 2xx so Snippe stops retrying). A database error → 500 (Snippe retries). Audit `sms_credits.purchase` (no actor; `source: webhook|reconcile`). |
| `GET /admin/sms/packages` | Admin. → `{items, purchases_enabled}` (inactive included). |
| `POST /admin/sms/packages` | Admin. `{name (1–60), credits (1–1 000 000), price (500–100 000 000 TZS), active? (true), sort_order? (0)}` → 201 `{package}`. Audit `sms_package.create`. |
| `PATCH /admin/sms/packages/{id}` | Admin. Any of the fields above → `{package}`; 404 unknown. No delete: `active: false` takes it off sale. Orders keep what they copied. Audit `sms_package.update` (before/after). |
| `GET /admin/sms/orders?status=&org_id=&limit=` | Admin. Every org's orders, newest first (limit 1–500, default 100) → `{items:[order + org_id, org_name], counts:{status: n}}`. |
| `POST /admin/sms/orders/reconcile` | Admin. Runs the reconciliation sweep now → `{checked, completed, failed, expired, mismatch, pending, errors}`. Audit `sms_orders.reconcile`. |
| `GET /admin/sms/purchases` | Admin. Beem bundles recorded → `{items:[{id, sms_count, cost, purchased_on, reference, note, admin_name, created_at}]}`. |
| `POST /admin/sms/purchases` | Admin. `{sms_count (≥1), cost (≥0, required; 0 = opening balance), purchased_on? (YYYY-MM-DD, default today), reference? (≤120), note? (≤500)}` → 201 `{purchase}`. Audit `platform_sms.purchase`. |
| `GET /admin/sms/stock` | Admin. → `{sms_bought, beem_cost, avg_cost_per_sms, sms_sent, credits_debited, uncharged_sent, stock, liability, credits_purchased, credits_granted, buffer, headroom, low_stock}`. `sms_sent` = credits debited + sent messages no credit paid for (exempt kinds, one SMS each); `stock` = bought − sent; `liability` = credits every org holds; `low_stock` = stock < liability + buffer. |
| `GET /admin/sms/margin?from=&to=` | Admin. Dates inclusive, default this month to today; `to` before `from` → 400. → `{from, to, orders, sales, credits_sold, snippe_fee (2.5% of sales), fee_percent, avg_cost_per_sms, beem_cost (credits sold × average Beem cost), margin, by_package:[…]}`. |

**Reconciliation job** (ticker in `cmd/api`, every minute, like the lifecycle job): pending orders older than 5 min and not checked in the last 2 min, up to 30 per sweep, are checked with `GET /v1/payments/{reference}` and settled exactly as a webhook would; an order still pending (or with no reference) 4 h after creation is `expired`.

The org ledger (`GET /admin/orgs/{id}/sms`) shows purchases with reason `purchase` and note `Order SMS-…`.

### Phase 27 correction (27 Sep 2026) — Snippe request body
`POST /org/sms-credits/orders` now sends Snippe the documented body: `{payment_type:"mobile", details:{amount, currency:"TZS"}, phone_number:"255…", customer:{firstname, lastname, email}, webhook_url, metadata:{order_code}}`. The customer is the ordering landlord; without a name or e-mail on the account → **422 `buyer_details_missing`**.

## Part 2 — Phase 30: offline contracts

Migration 000036: `contracts.is_offline` (default false), `backfill_batches.created_contract_id`.

| Route | Change |
|-------|--------|
| `POST /contracts/{id}/backfill` | **`from` now records an offline contract** instead of creating periods on this contract (supersedes Phase 29's `created_by_backfill_id` periods; existing ones still read and undo). The contract: `status:"ended"`, `is_offline:true`, the URL contract's renter and unit, `start_date = from`, `term_days` from `from` to the last covered day inclusive (`end_date` is the day after, like every contract), rent per payment period = `period_amount` (default: this contract's), cadence, payment period, due day and language inherited from this contract; `terms_snapshot_html` is a fixed SW/EN notice; no template, `snapshot_hash` or signatures. The last covered day is `min(until, day before this contract's start)`. Its periods are generated as for any contract (last one prorated), owned by the batch, and **all settled** (`paid`: one payment each, `paid_at` the period's due date but never after `until`; `waived`: with the note). If `until ≥` this contract's `start_date` its own periods due ≤ `until` are settled too, in the same batch. Errors: `from` ≥ this contract's start → 422 `from` ("would overlap the running contract that starts …"); `from` more than 3650 days ago → 422 `from`; `until` before `from` or in the future → 422 `until`; overlap with any other live contract of the unit (real or offline) → **409 `offline_overlap`** naming its dates. `from` while the history already exists is no longer a conflict. Response gains **`offline_contract_id`**; `created` counts the offline contract's periods. `dry_run` unchanged. Audit: `contract.create` for the offline contract (`after.offline:true`), `contract.backfill` gains `offline_contract_id`. |
| `GET /contracts…`, `GET /me/contracts`, contract reads | `contract.is_offline` (bool). |
| `GET /contracts/{id}/document` | For an offline contract: `offline:true`, `terms_html` the notice, empty `schedule` and `signatures`. |
| `POST /contracts/{id}/sign/otp`, `sign`, `signature-upload`, `witness-otp`, `activate`, `terminate`, `reissue`, `amend`, `notice`, `DELETE notice`, `moved-out`, `eviction`, `POST deposit`; `POST /payments` | On an offline contract → **409 `offline_contract`**. (Corrections go through the backfill's undo.) |
| `GET /contracts/{id}/backfills` | Lists batches whose `contract_id` **or** `created_contract_id` is the contract. Items gain `created_contract_id`, `offline_end` (last covered day). `from` / `created_periods` now describe the offline contract for new batches. |
| `POST /backfills/{id}/undo` | After reversing the money it soft-deletes the batch's periods **and the offline contract**; response gains `contract_removed`. `touched_since` also fires when anyone else has recorded money on the offline contract or edited/written off one of its periods since. |
| CSV `backfill` | `from` on a line records an offline contract for the renter (found by `renter_phone`) on the unit (`unit_code`) — **no running contract needed**. `period_amount` defaults to the renter's running contract's per-period rent, else it is a row error ("required when the renter has no running contract on this unit"). No `from` and no running contract → row error on `from`. Duplicate tenancy = same unit + renter in one file. Preview `resolved` gains `offline_end`, `offline_amount`; `created`/`periods`/`amount` include the offline periods (and the running contract's when `until` reaches it). |
| CSV import (all kinds) | A header naming a column twice → 400 `csv_header_mismatch` with `duplicate:[…]` (previously the later column was silently dropped). |

Excluded from the live book (`is_offline`): vacancy (`vacant_since`, `days_vacant`), holdovers, the historical occupancy series, projection history and last-rent baselines, the payments-import contract match. Included: revenue, collections, payments lists, exports, the renter directory (`RenterKnownToOrg`).
