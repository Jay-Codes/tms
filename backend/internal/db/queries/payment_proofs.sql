-- Proof of payment (SPEC §5.7, PLAN2 §16.1, FLOWS 7).
--
-- A proof is a claim the renter files and the landlord reviews. Nothing here
-- touches a schedule: acceptance runs the existing allocator and links the
-- payment it produced, which is why there is no "apply" query in this file.
--
-- Every row carries the identity block the proof shape renders — unit,
-- property, renter — so a review queue of twenty draws without twenty
-- follow-up requests.

-- The id is supplied rather than defaulted: it is minted when the upload ticket
-- is issued, because the object key is `{org_id}/{proof_id}.{ext}` and the key
-- has to exist before the file does.
-- name: CreatePaymentProof :one
INSERT INTO payment_proofs (
    id, org_id, contract_id, schedule_id, renter_user_id,
    amount, paid_at, method, reference, note,
    object_key, content_type, size_bytes
)
VALUES (
    sqlc.arg(id), sqlc.arg(org_id), sqlc.arg(contract_id), sqlc.narg(schedule_id), sqlc.arg(renter_user_id),
    sqlc.arg(amount), sqlc.arg(paid_at), sqlc.arg(method), sqlc.narg(reference), sqlc.narg(note),
    sqlc.arg(object_key), sqlc.arg(content_type), sqlc.arg(size_bytes)
)
RETURNING *;

-- GetPaymentProof is dual-scoped the way GetPayment is: the landlord supplies
-- org_id, the renter supplies renter_user_id, and the handler always supplies
-- one. Neither can name the other party's row.
-- guard-exempt: dual-scoped — org_id for GET /proofs/{id}, renter_user_id for the renter's own; the handler always supplies one.
-- name: GetPaymentProof :one
SELECT pp.*,
       c.unit_id,
       u.name  AS unit_name,
       pr.name AS property_name,
       ru.full_name AS renter_name,
       rv.full_name AS reviewed_by_name
FROM payment_proofs pp
JOIN contracts c   ON c.id = pp.contract_id AND c.org_id = pp.org_id
JOIN units u       ON u.id = c.unit_id AND u.org_id = c.org_id
JOIN properties pr ON pr.id = u.property_id AND pr.org_id = c.org_id
JOIN users ru      ON ru.id = pp.renter_user_id
LEFT JOIN users rv ON rv.id = pp.reviewed_by_user_id
WHERE pp.id = sqlc.arg(id)
  AND pp.org_id = COALESCE(sqlc.narg(org_id)::uuid, pp.org_id)
  AND pp.renter_user_id = COALESCE(sqlc.narg(renter_user_id)::uuid, pp.renter_user_id);

-- ListPaymentProofs is the landlord's review queue: one status at a time,
-- oldest claim first, because the renter who has waited longest is the one
-- owed an answer.
-- name: ListPaymentProofs :many
SELECT pp.*,
       c.unit_id,
       u.name  AS unit_name,
       pr.name AS property_name,
       ru.full_name AS renter_name,
       rv.full_name AS reviewed_by_name
FROM payment_proofs pp
JOIN contracts c   ON c.id = pp.contract_id AND c.org_id = pp.org_id
JOIN units u       ON u.id = c.unit_id AND u.org_id = c.org_id
JOIN properties pr ON pr.id = u.property_id AND pr.org_id = c.org_id
JOIN users ru      ON ru.id = pp.renter_user_id
LEFT JOIN users rv ON rv.id = pp.reviewed_by_user_id
WHERE pp.org_id = sqlc.arg(org_id)
  AND pp.status = sqlc.arg(status)
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (pp.created_at, pp.id) > (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY pp.created_at, pp.id
LIMIT sqlc.arg(row_limit);

-- ListMyPaymentProofs is the renter's own history, newest first, across every
-- org they rent from — the same scope GET /me/payments has.
-- guard-exempt: renter-scoped — the caller's own claims span every org they rent from.
-- name: ListMyPaymentProofs :many
SELECT pp.*,
       c.unit_id,
       u.name  AS unit_name,
       pr.name AS property_name,
       ru.full_name AS renter_name,
       rv.full_name AS reviewed_by_name
FROM payment_proofs pp
JOIN contracts c   ON c.id = pp.contract_id AND c.org_id = pp.org_id
JOIN units u       ON u.id = c.unit_id AND u.org_id = c.org_id
JOIN properties pr ON pr.id = u.property_id AND pr.org_id = c.org_id
JOIN users ru      ON ru.id = pp.renter_user_id
LEFT JOIN users rv ON rv.id = pp.reviewed_by_user_id
WHERE pp.renter_user_id = sqlc.arg(renter_user_id)
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (pp.created_at, pp.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY pp.created_at DESC, pp.id DESC
LIMIT sqlc.arg(row_limit);

-- CountSubmittedProofs drives the nav badge. It reads the partial index and
-- nothing else, so a landlord's every page load costs one index scan.
-- name: CountSubmittedProofs :one
SELECT count(*) FROM payment_proofs
WHERE org_id = sqlc.arg(org_id) AND status = 'submitted';

-- CountProofsToday is the renter's daily submission ceiling, checked in
-- Postgres as well as in Redis: the limiter fails open when Redis is down
-- (SPEC §8), and the ceiling on a write must not.
-- guard-exempt: renter-scoped — the caller's own claims across every org they rent from.
-- name: CountProofsToday :one
SELECT count(*) FROM payment_proofs
WHERE renter_user_id = sqlc.arg(renter_user_id) AND created_at >= now() - INTERVAL '1 day';

-- WithdrawPaymentProof is the renter taking a claim back. It is the one place
-- a proof row is really deleted, and it is deliberate: an unreviewed claim the
-- renter retracted is not a fact anybody needs to keep, and the audit row
-- records that it happened. The `submitted` predicate is the lock — a decided
-- proof matches nothing and the handler answers 409.
-- guard-exempt: renter-scoped — a renter may only withdraw their own claim.
-- name: WithdrawPaymentProof :one
DELETE FROM payment_proofs
WHERE id = sqlc.arg(id) AND renter_user_id = sqlc.arg(renter_user_id) AND status = 'submitted'
RETURNING *;

-- AcceptPaymentProof stamps the review and links the payment the allocator
-- produced. `status = 'submitted'` in the predicate is what makes a second
-- acceptance a 409 rather than a second payment.
-- name: AcceptPaymentProof :one
UPDATE payment_proofs
SET status = 'accepted',
    payment_id = sqlc.arg(payment_id),
    reviewed_by_user_id = sqlc.arg(reviewed_by_user_id),
    reviewed_at = now()
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND status = 'submitted'
RETURNING *;

-- name: RejectPaymentProof :one
UPDATE payment_proofs
SET status = 'rejected',
    rejection_reason = sqlc.arg(rejection_reason),
    reviewed_by_user_id = sqlc.arg(reviewed_by_user_id),
    reviewed_at = now()
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND status = 'submitted'
RETURNING *;
