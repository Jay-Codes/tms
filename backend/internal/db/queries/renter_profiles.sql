-- renter_profiles belongs to a user, not to an org (a renter can hold
-- contracts with several orgs), so these queries are keyed by user_id.
--
-- nida_number is encrypted at rest with pgcrypto. The symmetric key is passed
-- as a parameter (config NIDA_ENC_KEY) and never stored.

-- name: UpsertRenterProfile :one
INSERT INTO renter_profiles (user_id, full_name, nida_number_enc, next_of_kin_name, next_of_kin_phone)
VALUES (
    sqlc.arg(user_id),
    sqlc.arg(full_name),
    CASE WHEN sqlc.narg(nida_number)::text IS NULL THEN NULL
         ELSE pgp_sym_encrypt(sqlc.narg(nida_number)::text, sqlc.arg(enc_key)::text) END,
    sqlc.narg(next_of_kin_name),
    sqlc.narg(next_of_kin_phone)
)
ON CONFLICT (user_id) DO UPDATE
SET full_name         = EXCLUDED.full_name,
    nida_number_enc   = COALESCE(EXCLUDED.nida_number_enc, renter_profiles.nida_number_enc),
    next_of_kin_name  = COALESCE(EXCLUDED.next_of_kin_name, renter_profiles.next_of_kin_name),
    next_of_kin_phone = COALESCE(EXCLUDED.next_of_kin_phone, renter_profiles.next_of_kin_phone)
RETURNING *;

-- name: GetRenterProfileDecrypted :one
SELECT p.id, p.user_id, p.full_name,
       CASE WHEN p.nida_number_enc IS NULL THEN NULL
            ELSE pgp_sym_decrypt(p.nida_number_enc, sqlc.arg(enc_key)::text) END::text AS nida_number,
       p.next_of_kin_name, p.next_of_kin_phone, p.kyc_status, p.kyc_doc_object_key,
       p.created_at, p.updated_at
FROM renter_profiles p
WHERE p.user_id = sqlc.arg(user_id) AND p.deleted_at IS NULL;
