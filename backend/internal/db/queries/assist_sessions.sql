-- assist_sessions is the durable shape of a landlord-assisted onboarding
-- (Phase 18, SPEC §5.15, FLOWS 2b). The code itself is never here — it lives
-- only in the Redis slot the SMS path writes — so these queries move the
-- session's status and its stamps, nothing else.

-- name: CreateAssistSession :one
INSERT INTO assist_sessions (
    org_id, unit_id, phone, purpose, started_by_user_id,
    code_issued_count, last_code_at, expires_at
)
VALUES (
    sqlc.arg(org_id), sqlc.arg(unit_id), sqlc.arg(phone), sqlc.arg(purpose),
    sqlc.arg(started_by_user_id), 1, now(), sqlc.arg(expires_at)
)
RETURNING *;

-- name: GetAssistSession :one
SELECT s.*,
       u.unit_code                          AS unit_code,
       u.name                               AS unit_name,
       COALESCE(r.full_name, '')::text      AS renter_name,
       COALESCE(lr.status,   '')::text      AS link_request_status
FROM assist_sessions s
JOIN units u ON u.id = s.unit_id
LEFT JOIN users r ON r.id = s.renter_user_id
LEFT JOIN unit_link_requests lr ON lr.id = s.link_request_id
WHERE s.org_id = sqlc.arg(org_id) AND s.id = sqlc.arg(id);

-- ListOpenAssistSessions is the landlord's screen: the org's sessions that are
-- still live, newest first. An expired row is closed in every sense but the
-- column, so the expiry is part of the filter rather than left to a sweep.
-- name: ListOpenAssistSessions :many
SELECT s.*,
       u.unit_code                          AS unit_code,
       u.name                               AS unit_name,
       COALESCE(r.full_name, '')::text      AS renter_name,
       COALESCE(lr.status,   '')::text      AS link_request_status
FROM assist_sessions s
JOIN units u ON u.id = s.unit_id
LEFT JOIN users r ON r.id = s.renter_user_id
LEFT JOIN unit_link_requests lr ON lr.id = s.link_request_id
WHERE s.org_id = sqlc.arg(org_id) AND s.status = 'open' AND s.expires_at > now()
ORDER BY s.created_at DESC, s.id DESC
LIMIT sqlc.arg(row_limit);

-- FindOpenAssistSessionByPhone is the 409 `assist_open` check: one open
-- session per (org, phone), which the partial unique index enforces and this
-- read turns into a useful answer (the id of the session already running).
-- name: FindOpenAssistSessionByPhone :one
SELECT * FROM assist_sessions
WHERE org_id = sqlc.arg(org_id) AND phone = sqlc.arg(phone)
  AND status = 'open' AND expires_at > now();

-- IssueAssistCode records one reveal: the count the per-session cap reads, the
-- timestamp, and the 30-minute window pushed out again. It refuses a closed or
-- expired session in the same statement, so the cap cannot be raced.
-- name: IssueAssistCode :one
UPDATE assist_sessions
SET code_issued_count = code_issued_count + 1,
    last_code_at      = now(),
    expires_at        = sqlc.arg(expires_at)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
  AND status = 'open' AND expires_at > now()
RETURNING *;

-- name: CloseAssistSession :one
UPDATE assist_sessions
SET status = 'closed', closed_at = COALESCE(closed_at, now())
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
RETURNING *;

-- StampAssistRenter attaches the account the renter created (or logged into)
-- to the session the landlord opened for their number.
--
-- guard-exempt: the hook runs on POST /auth/otp/verify and POST /auth/register/renter,
-- which have no org context at all — the session id came from the Redis marker
-- the assisted issue wrote, which is itself org-scoped.
-- name: StampAssistRenter :exec
UPDATE assist_sessions SET renter_user_id = sqlc.arg(renter_user_id)
WHERE id = sqlc.arg(id) AND status = 'open';

-- FindOpenAssistSessionForLink finds the session a link request belongs to:
-- same org, same unit, and the renter who just applied — matched on the
-- account when the session already knows it, else on the phone they verified.
-- name: FindOpenAssistSessionForLink :one
SELECT * FROM assist_sessions
WHERE org_id = sqlc.arg(org_id) AND unit_id = sqlc.arg(unit_id)
  AND status = 'open' AND expires_at > now()
  AND (renter_user_id = sqlc.arg(renter_user_id) OR phone = sqlc.arg(phone))
ORDER BY created_at DESC
LIMIT 1;

-- name: StampAssistLinkRequest :exec
UPDATE assist_sessions
SET link_request_id = sqlc.arg(link_request_id),
    renter_user_id  = COALESCE(renter_user_id, sqlc.narg(renter_user_id))
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND status = 'open';

-- GetPublicAssistSession backs GET /public/assist/{id}: the renter's own
-- device has the session id from the QR and nothing else, so the answer is the
-- unit code and the purpose — never the phone, never the org's rows.
--
-- guard-exempt: unauthenticated lookup by an unguessable session id; the
-- projection is the unit code and purpose alone (SPEC §5.15).
-- name: GetPublicAssistSession :one
SELECT s.id, s.purpose, s.status, s.expires_at, u.unit_code
FROM assist_sessions s
JOIN units u ON u.id = s.unit_id
WHERE s.id = sqlc.arg(id);
