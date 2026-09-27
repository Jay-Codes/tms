-- Phase 19 — NIDA reveal, the platform user directory, name corrections.
--
-- The reveal itself needs no query of its own: it reads the profile through the
-- existing GetRenterProfileDecrypted and the relationship through GetOrgRenter,
-- which is the one place that decides what "a renter this org knows" means.
-- What is new here is the renter's own view of who looked, the signed-contract
-- guard on a landlord rename, and the cross-org directory the platform admin
-- searches.

-- ------------------------------------------------ 19.1 who has seen my NIDA --

-- ListNidaRevealsForUser is the renter's own deterrent: every reveal of their
-- number, newest first, read straight off the append-only trail rather than
-- from a second table that could disagree with it. The `reason` an actor typed
-- is deliberately not selected — it is the org's note to itself, and the renter
-- is told who looked and when, not what the landlord wrote about them.
-- guard-exempt: the renter's own audit rows, keyed by entity_id = their user id.
-- name: ListNidaRevealsForUser :many
SELECT a.at, COALESCE(a.after->>'actor_kind', '')::text AS actor_kind,
       COALESCE(o.name, '')::text AS org_name
FROM audit_log a
LEFT JOIN orgs o ON o.id = a.org_id
WHERE a.entity_type = 'user' AND a.entity_id = sqlc.arg(user_id)
  AND a.action = 'renter.nida_reveal'
ORDER BY a.at DESC, a.id DESC
LIMIT sqlc.arg(row_limit);

-- ------------------------------------------------------ 19.3 name corrections --

-- RenterHasSignedAnywhere is the guard on PATCH /renters/{user_id}: a landlord
-- may fix a typo only before the renter has put their name to anything. It
-- crosses orgs on purpose — a signature with another landlord is still this
-- renter's own act, and the name on it is theirs to correct from their Profile.
-- guard-exempt: deliberately cross-org — a signature anywhere blocks the rename.
-- name: RenterHasSignedAnywhere :one
SELECT EXISTS (
    SELECT 1 FROM contract_signatures
    WHERE user_id = sqlc.arg(user_id) AND party = 'renter'
) AS signed;

-- RenterSignedElsewhere backs the post-signing rename (Phase 21): a landlord
-- may correct the name of a renter who has signed only with them, never one
-- whose signature sits on another landlord's document.
-- guard-exempt: deliberately cross-org — it asks about every org but the caller's.
-- name: RenterSignedElsewhere :one
SELECT EXISTS (
    SELECT 1 FROM contract_signatures
    WHERE user_id = sqlc.arg(user_id) AND party = 'renter'
      AND org_id <> sqlc.arg(org_id)
) AS signed;

-- SetRenterProfileFullName writes the profile half of a rename. The users half
-- goes through the existing SetUserFullName, so both callers write the same two
-- rows in the same transaction.
-- name: SetRenterProfileFullName :one
UPDATE renter_profiles SET full_name = sqlc.arg(full_name)
WHERE user_id = sqlc.arg(user_id) AND deleted_at IS NULL
RETURNING *;

-- SetOrgMemberRole backs the role half of PATCH /org/members/{id}. The
-- last-owner check runs in the handler against CountActiveOwners inside the
-- same transaction, so a concurrent demotion cannot empty an org of owners.
-- name: SetOrgMemberRole :one
UPDATE org_members SET role = sqlc.arg(role)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- ---------------------------------------------- 19.2 platform user directory --

-- AdminListUsers is the support desk's first question: who is this phone
-- number. It crosses orgs by design and is reachable only behind
-- auth.RequireAdmin.
--
-- `q` is matched three ways at once because a caller pastes whatever the person
-- on the telephone reads out: a phone (already normalised to +255… by the
-- handler, so this is a prefix on the stored form), an e-mail (lower-cased
-- prefix) or a name (ILIKE prefix). It is never a leading-wildcard search —
-- that would table-scan every user on the platform for every keystroke.
--
-- No NIDA column appears here, masked or otherwise (SPEC §8): the list is a
-- search result, and a number nobody asked for must not travel in one.
-- guard-exempt: platform-admin cross-org directory (SPEC §5.10).
-- name: AdminListUsers :many
SELECT u.id, u.kind, u.full_name, u.phone, u.email, u.status, u.created_at,
       (SELECT count(*) FROM contracts c
        WHERE c.renter_user_id = u.id AND c.deleted_at IS NULL
          AND c.status IN ('active', 'expiring'))::bigint AS contracts_live,
       COALESCE((SELECT rp.kyc_status FROM renter_profiles rp
                 WHERE rp.user_id = u.id AND rp.deleted_at IS NULL), 'incomplete')::text AS kyc_status
FROM users u
WHERE u.deleted_at IS NULL
  AND (sqlc.narg(kind)::text IS NULL OR u.kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(status)::text IS NULL OR u.status = sqlc.narg(status)::text)
  AND (sqlc.narg(q)::text IS NULL
       OR u.full_name ILIKE sqlc.narg(q)::text || '%'
       OR lower(COALESCE(u.email, '')) LIKE lower(sqlc.narg(q)::text) || '%'
       OR (sqlc.narg(phone)::text IS NOT NULL
           AND COALESCE(u.phone, '') LIKE sqlc.narg(phone)::text || '%'))
  AND (sqlc.narg(org_id)::uuid IS NULL
       OR EXISTS (SELECT 1 FROM org_members m
                  WHERE m.user_id = u.id AND m.org_id = sqlc.narg(org_id)::uuid AND m.deleted_at IS NULL)
       OR EXISTS (SELECT 1 FROM unit_link_requests lr
                  WHERE lr.renter_user_id = u.id AND lr.org_id = sqlc.narg(org_id)::uuid
                    AND lr.deleted_at IS NULL)
       OR EXISTS (SELECT 1 FROM contracts c2
                  WHERE c2.renter_user_id = u.id AND c2.org_id = sqlc.narg(org_id)::uuid
                    AND c2.deleted_at IS NULL))
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (u.created_at, u.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY u.created_at DESC, u.id DESC
LIMIT sqlc.arg(row_limit);

-- AdminGetUser is the same row for one id, without the search predicates.
-- guard-exempt: platform-admin cross-org directory.
-- name: AdminGetUser :one
SELECT u.id, u.kind, u.full_name, u.phone, u.email, u.status, u.created_at,
       (SELECT count(*) FROM contracts c
        WHERE c.renter_user_id = u.id AND c.deleted_at IS NULL
          AND c.status IN ('active', 'expiring'))::bigint AS contracts_live,
       COALESCE((SELECT rp.kyc_status FROM renter_profiles rp
                 WHERE rp.user_id = u.id AND rp.deleted_at IS NULL), 'incomplete')::text AS kyc_status
FROM users u
WHERE u.id = sqlc.arg(id) AND u.deleted_at IS NULL;

-- AdminUserOrgs is the `orgs[]` block of a directory row: one row per org this
-- person is connected to, from either side of the platform. `role` is set for a
-- member; `relationship` is set for a renter and says which of the three states
-- the tenancy is in — `renting` (a live contract), `past` (a finished one) or
-- `applied` (a link request and nothing more).
--
-- It takes an array of user ids so one page of the directory costs one round
-- trip rather than one per row.
-- guard-exempt: platform-admin cross-org directory.
-- name: AdminUserOrgs :many
SELECT x.user_id, x.org_id, o.name AS org_name, o.status AS org_status,
       COALESCE(max(x.role), '')::text AS role,
       COALESCE(CASE WHEN bool_or(x.live) THEN 'renting'
                     WHEN bool_or(x.ended) THEN 'past'
                     WHEN bool_or(x.applied) THEN 'applied'
                     ELSE NULL END, '')::text AS relationship
FROM (
    SELECT m.user_id, m.org_id, m.role,
           FALSE AS live, FALSE AS ended, FALSE AS applied
    FROM org_members m
    WHERE m.user_id = ANY (sqlc.arg(user_ids)::uuid[]) AND m.deleted_at IS NULL
    UNION ALL
    SELECT c.renter_user_id, c.org_id, NULL::text,
           c.status IN ('active', 'expiring', 'pending_signature'),
           c.status IN ('ended', 'terminated'), FALSE
    FROM contracts c
    WHERE c.renter_user_id = ANY (sqlc.arg(user_ids)::uuid[]) AND c.deleted_at IS NULL
    UNION ALL
    SELECT lr.renter_user_id, lr.org_id, NULL::text, FALSE, FALSE, TRUE
    FROM unit_link_requests lr
    WHERE lr.renter_user_id = ANY (sqlc.arg(user_ids)::uuid[]) AND lr.deleted_at IS NULL
) x
JOIN orgs o ON o.id = x.org_id AND o.deleted_at IS NULL
GROUP BY x.user_id, x.org_id, o.name, o.status
ORDER BY o.name, x.org_id;

-- AdminUserLinkRequests is the detail page's tenancy history, newest first.
-- guard-exempt: platform-admin cross-org directory.
-- name: AdminUserLinkRequests :many
SELECT lr.id, lr.org_id, o.name AS org_name, lr.unit_id, un.name AS unit_name,
       p.name AS property_name, lr.status, lr.rejection_reason,
       lr.start_date, lr.end_date, lr.created_at
FROM unit_link_requests lr
JOIN orgs o        ON o.id = lr.org_id
JOIN units un      ON un.id = lr.unit_id
JOIN properties p  ON p.id = un.property_id
WHERE lr.renter_user_id = sqlc.arg(user_id) AND lr.deleted_at IS NULL
ORDER BY lr.created_at DESC, lr.id DESC
LIMIT sqlc.arg(row_limit);

-- AdminUserContracts lists every tenancy of this renter across the platform.
-- guard-exempt: platform-admin cross-org directory.
-- name: AdminUserContracts :many
SELECT c.id, c.org_id, o.name AS org_name, un.name AS unit_name, p.name AS property_name,
       c.status, c.start_date, c.end_date, c.rent_amount, c.created_at
FROM contracts c
JOIN orgs o       ON o.id = c.org_id
JOIN units un     ON un.id = c.unit_id
JOIN properties p ON p.id = un.property_id
WHERE c.renter_user_id = sqlc.arg(user_id) AND c.deleted_at IS NULL
ORDER BY c.start_date DESC, c.id DESC;

-- AdminUserPaymentsSummary is the money line of the detail page: what this
-- renter has actually paid, anywhere. Reversed rows are excluded — the summary
-- answers "how much has reached a landlord", and a reversal took it back.
-- guard-exempt: platform-admin cross-org directory.
-- name: AdminUserPaymentsSummary :one
SELECT count(*)::bigint AS count,
       COALESCE(sum(pm.amount), 0)::bigint AS total,
       max(pm.paid_at) AS last_paid_at
FROM payments pm
JOIN contracts c ON c.id = pm.contract_id
WHERE c.renter_user_id = sqlc.arg(user_id)
  AND pm.status = 'recorded' AND pm.deleted_at IS NULL;

-- AdminUserMemberships is the org_user half of the detail page.
-- guard-exempt: platform-admin cross-org directory.
-- name: AdminUserMemberships :many
SELECT m.id, m.org_id, o.name AS org_name, o.status AS org_status, m.role, m.status, m.created_at
FROM org_members m
JOIN orgs o ON o.id = m.org_id
WHERE m.user_id = sqlc.arg(user_id) AND m.deleted_at IS NULL
ORDER BY m.created_at, m.id;

-- AdminListAuditForUser is every row where this person is the actor *or* the
-- subject: "what did they do" and "what was done to them" are the same question
-- on a support call, and splitting them into two lists would hide the half the
-- operator did not think to open.
-- guard-exempt: platform-admin cross-org audit read.
-- name: AdminListAuditForUser :many
SELECT a.id, a.org_id, a.actor_user_id, a.action, a.entity_type, a.entity_id,
       a.before, a.after, a.ip, a.user_agent, a.at,
       au.full_name AS actor_name,
       COALESCE(o.name, '')::text AS org_name
FROM audit_log a
LEFT JOIN users au ON au.id = a.actor_user_id
LEFT JOIN orgs o   ON o.id = a.org_id
WHERE (a.actor_user_id = sqlc.arg(user_id)
       OR (a.entity_type = 'user' AND a.entity_id = sqlc.arg(user_id)))
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (a.at, a.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY a.at DESC, a.id DESC
LIMIT sqlc.arg(row_limit);

-- AdminSetUserStatus is the suspension switch. It only ever moves a row that is
-- not already in the target state, so a second suspend finds no row and the
-- handler answers 409 rather than writing a second audit row for nothing.
-- guard-exempt: platform-admin cross-org write (SPEC §5.10).
-- name: AdminSetUserStatus :one
UPDATE users SET status = sqlc.arg(status)
WHERE id = sqlc.arg(id) AND deleted_at IS NULL AND status <> sqlc.arg(status)
RETURNING *;

-- RevokeSessionsForUser revokes every live session of one account, whichever
-- audience or org it was issued for: a suspended person must be out of the
-- product, not merely out of one tenant. The returned hashes let the caller
-- evict the Redis copies after the commit.
-- guard-exempt: platform-admin cross-org write; the subject is the user id.
-- name: RevokeSessionsForUser :many
UPDATE sessions SET revoked_at = now()
WHERE user_id = sqlc.arg(user_id) AND revoked_at IS NULL
RETURNING token_hash;
