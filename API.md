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
| `POST /contracts/{id}/activate` (org) | requires a renter signature → else **412** `renter_signature_required`; inserts the landlord signature row (method `otp_accept`, user = session), generates all `payment_schedules` via `contract.Generate`, status `active`, `activated_at`, unit → `occupied` (clears override; the link request stays `approved`). Queues SMS `welcome`. Wrong status → 409 `not_pending_signature`. |
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
- **Snapshot:** `terms_snapshot_html` is the template body sanitized again and rendered with `{{renter_name}} {{unit}} {{property}} {{rent}} {{start_date}} {{end_date}} {{payment_period}} {{org_name}} {{term_days}} {{due_day}}`; **values are HTML-escaped, the body is not** — a renter called `Asha <script>` becomes text. `{{rent}}` is formatted `TZS 250,000`, `{{payment_period}}` is `Monthly (30 days)`, an unknown variable resolves to blank. `snapshot_hash` is the hex SHA-256 of `terms_snapshot_html|unit_id|renter_user_id|rent_amount|rent_period_days|payment_period_days|term_days|start_date|end_date|due_day` (a NULL due day contributes an empty field). `GET /verify` recomputes it from the stored row, so editing `terms_snapshot_html` or any commercial column in SQL flips `valid` to false.
- **Approval creates the contract, or the approval fails.** If the contract cannot be written (unpriced unit, no template, unit taken) the approval is refused whole with that contract error, and nothing is committed — a renter is never told "approved" with nothing to sign. The same holds for auto-approve at application time.
- **Backfill:** `POST /link-requests/{id}/approve` on an already-`approved` request with no contract creates one (`200 {request, contract}`); with a contract it is a `409 not_pending`. Idempotence is backed by `contracts.link_request_id`.
- **Signing:** the OTP is keyed to the contract, so two contracts awaiting signature never share or cancel each other's code; the SMS names the unit. The signature row records `otp_ref`, the caller's IP and user agent, and the hash that was on screen. A drawn signature is optional: `POST /signature-upload` presigns a 10-minute PUT for `signatures/{org_id}/{contract_id}/renter.png` (image/png, ≤512 KiB) and `POST /sign` accepts exactly that key, refusing (400) any other key or a key with no object behind it; supplying one sets `method: "drawn"`. `contract_signatures` is append-only with a unique index on `(contract_id, party)`.
- **Activation** writes every `payment_schedules` row for the whole term in one transaction: one row per cadence interval, the last truncated, each amount prorated from the contract's own snapshotted rent basis (`rent × days / rent_period_days`, rounded half away from zero). 180 days at a 45-day cadence → four 45-day rows; 100 days at 30 → 30/30/30/10. `paid_amount` starts at 0 (Phase 5 writes it).
- **`GET /contracts/{id}/document`** returns `org:{display_name,logo_url,letterhead_url,footer_text,theme}` (images presigned 1 h), `terms_html`, `parties`, `signatures[]` (with `signature_image_url` presigned 1 h when a drawn image exists), `snapshot_hash` and `generated_at`. `schedule` is the stored `payment_schedules` once they exist, and the same generator's preview before activation — the renter must see the money before agreeing to it.
- **Notifications:** kinds `contract_ready`, `welcome`, `contract_terminated`, dedupe key `{kind}:{contract_id}`, Swahili/English per `orgs.settings.sms_language`. An approval therefore writes **two** rows: `link_approved` and `contract_ready`.
- **Branding:** presigned reads last 1 hour (a document is read, printed and re-read). Upload keys are fixed per org and asset — `branding/{org_id}/logo.{png|jpg}` and `.../letterhead.{png|jpg}` — so a re-upload replaces the image; `.../complete` StatObjects what landed and rejects a wrong type or a file over 2 MiB (deleting it). `DELETE` clears the reference and removes the object. `theme.primary_color` must be a 6-digit hex (`#1B4DB1`); `theme.font_id` is one of `bricolage|archivo|instrument|hanken`. An explicit `document_footer_text: null` clears the footer. With MinIO unreachable the upload/delete routes answer **503**; a read simply omits the URL.
- **Templates:** an org has at most one default (partial unique index). Promoting one demotes the incumbent; clearing the only default is a **400** (promote another instead). Bodies are sanitized on write *and* again on render, so a body stored before a policy change cannot escape the current allowlist; a body with nothing left after sanitizing is a **400**. Editing a template never touches an existing contract — the contract carries its own snapshot.
- **Seeding:** every org gets one default template, "Standard tenancy agreement". New orgs get it at `POST /orgs`; orgs that already existed get a byte-identical copy from migration 000005 (idempotent — it inserts only where an org has no live template). `internal/contract.DefaultTemplateBody` and the migration are kept in step by a test.

Phase 4 audit actions: `contract_template.create`, `contract_template.update`, `contract_template.delete`, `contract.create`, `contract.sign`, `contract.activate`, `contract.activate_landlord_recorded`, `contract.terminate`, `contract.lifecycle_run`, `org.branding_update`, `org.branding_asset`. Requesting a signing code reuses `auth.otp_send` with entity type `contract`.
