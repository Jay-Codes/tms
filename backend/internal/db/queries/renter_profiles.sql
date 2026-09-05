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
       -- Empty rather than NULL: "no number on file" and "a number we could
       -- not read" are the same thing to the caller, and the masking helper
       -- already renders a short value as no mask at all.
       COALESCE(
           CASE WHEN p.nida_number_enc IS NULL THEN NULL
                ELSE pgp_sym_decrypt(p.nida_number_enc, sqlc.arg(enc_key)::text) END,
           '')::text AS nida_number,
       p.next_of_kin_name, p.next_of_kin_phone, p.kyc_status, p.kyc_doc_object_key,
       p.created_at, p.updated_at
FROM renter_profiles p
WHERE p.user_id = sqlc.arg(user_id) AND p.deleted_at IS NULL;

-- SetRenterKycStatus is the derivation from the PUT handler (API.md: a profile
-- carrying NIDA and next of kin is `submitted`). `verified` is set by an
-- operator and is never lowered here.
-- name: SetRenterKycStatus :one
UPDATE renter_profiles
SET kyc_status = sqlc.arg(kyc_status)
WHERE user_id = sqlc.arg(user_id) AND deleted_at IS NULL
  AND kyc_status <> 'verified'
RETURNING *;

-- name: SetRenterKycDoc :one
UPDATE renter_profiles
SET kyc_doc_object_key = sqlc.arg(kyc_doc_object_key)
WHERE user_id = sqlc.arg(user_id) AND deleted_at IS NULL
RETURNING *;

-- EnsureRenterProfile creates the empty profile row a renter gets on first
-- read, so GET /me/profile answers with a shape rather than a 404.
-- name: EnsureRenterProfile :one
INSERT INTO renter_profiles (user_id, full_name)
VALUES (sqlc.arg(user_id), sqlc.arg(full_name))
ON CONFLICT (user_id) DO UPDATE SET user_id = renter_profiles.user_id
RETURNING *;
