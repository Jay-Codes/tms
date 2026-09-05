-- contract_signatures is append-only evidence: one row per party, carrying the
-- hash that was on screen when it was signed plus the OTP reference, IP and
-- user agent (SPEC §5.5 "Digital signing"). There is no update and no delete —
-- a disputed contract is terminated and replaced, never edited.

-- name: CreateContractSignature :one
INSERT INTO contract_signatures (
    org_id, contract_id, party, user_id, method, otp_ref,
    signature_object_key, snapshot_hash, ip, user_agent
)
VALUES (
    sqlc.arg(org_id), sqlc.arg(contract_id), sqlc.arg(party), sqlc.narg(user_id),
    sqlc.arg(method), sqlc.narg(otp_ref), sqlc.narg(signature_object_key),
    sqlc.arg(snapshot_hash), sqlc.narg(ip), sqlc.narg(user_agent)
)
RETURNING *;

-- name: ListContractSignatures :many
SELECT sg.id, sg.org_id, sg.contract_id, sg.party, sg.user_id, sg.method,
       sg.otp_ref, sg.signature_object_key, sg.snapshot_hash, sg.signed_at,
       COALESCE(u.full_name, '')::text AS signer_name,
       u.phone AS signer_phone
FROM contract_signatures sg
LEFT JOIN users u ON u.id = sg.user_id
WHERE sg.org_id = sqlc.arg(org_id) AND sg.contract_id = sqlc.arg(contract_id)
ORDER BY sg.signed_at, sg.party;

-- name: CountContractSignatures :one
SELECT count(*) FROM contract_signatures
WHERE org_id = sqlc.arg(org_id) AND contract_id = sqlc.arg(contract_id)
  AND party = sqlc.arg(party);
