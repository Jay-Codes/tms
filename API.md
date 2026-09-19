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
