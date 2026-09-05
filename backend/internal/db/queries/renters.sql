-- The landlord's renter directory (SPEC §5.4). "A renter this org knows" means
-- a renter who has ever made a link request to one of the org's units, or who
-- holds a contract with it — nothing else makes a renter visible to an org, so
-- these are the queries that decide cross-org visibility.

-- name: ListOrgRenters :many
SELECT u.id AS user_id, u.phone, u.email, u.full_name AS user_name, u.created_at,
       COALESCE(rp.full_name, u.full_name)::text AS profile_name,
       COALESCE(rp.kyc_status, 'incomplete')::text AS kyc_status,
       (rp.kyc_doc_object_key IS NOT NULL)::boolean AS kyc_doc_uploaded
FROM users u
JOIN LATERAL (
    SELECT min(seen) AS first_seen FROM (
        SELECT lr.created_at AS seen FROM unit_link_requests lr
        WHERE lr.renter_user_id = u.id AND lr.org_id = sqlc.arg(org_id) AND lr.deleted_at IS NULL
        UNION ALL
        SELECT c.created_at FROM contracts c
        WHERE c.renter_user_id = u.id AND c.org_id = sqlc.arg(org_id) AND c.deleted_at IS NULL
    ) s
) rel ON rel.first_seen IS NOT NULL
LEFT JOIN renter_profiles rp ON rp.user_id = u.id AND rp.deleted_at IS NULL
WHERE u.kind = 'renter' AND u.deleted_at IS NULL
  AND (sqlc.narg(q)::text IS NULL
       OR u.full_name ILIKE '%' || sqlc.narg(q)::text || '%'
       OR COALESCE(rp.full_name, '') ILIKE '%' || sqlc.narg(q)::text || '%'
       OR COALESCE(u.phone, '') ILIKE '%' || sqlc.narg(q)::text || '%')
  AND (sqlc.narg(kyc_status)::text IS NULL
       OR COALESCE(rp.kyc_status, 'incomplete') = sqlc.narg(kyc_status)::text)
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (u.created_at, u.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY u.created_at DESC, u.id DESC
LIMIT sqlc.arg(row_limit);

-- GetOrgRenter is the directory's single-row form: it answers only for a
-- renter this org already knows, so an unrelated renter is a 404 rather than a
-- probe that confirms the account exists.
-- name: GetOrgRenter :one
SELECT u.id AS user_id, u.phone, u.email, u.full_name AS user_name, u.created_at,
       COALESCE(rp.full_name, u.full_name)::text AS profile_name,
       COALESCE(rp.kyc_status, 'incomplete')::text AS kyc_status,
       (rp.kyc_doc_object_key IS NOT NULL)::boolean AS kyc_doc_uploaded
FROM users u
LEFT JOIN renter_profiles rp ON rp.user_id = u.id AND rp.deleted_at IS NULL
WHERE u.id = sqlc.arg(user_id) AND u.kind = 'renter' AND u.deleted_at IS NULL
  AND (EXISTS (SELECT 1 FROM unit_link_requests lr
               WHERE lr.renter_user_id = u.id AND lr.org_id = sqlc.arg(org_id) AND lr.deleted_at IS NULL)
    OR EXISTS (SELECT 1 FROM contracts c
               WHERE c.renter_user_id = u.id AND c.org_id = sqlc.arg(org_id) AND c.deleted_at IS NULL));

-- name: ListRenterUnitsForOrg :many
SELECT lr.unit_id, u.name AS unit_name, p.name AS property_name, lr.status AS link_status
FROM unit_link_requests lr
JOIN units u      ON u.id = lr.unit_id AND u.org_id = lr.org_id
JOIN properties p ON p.id = u.property_id AND p.org_id = lr.org_id
WHERE lr.org_id = sqlc.arg(org_id) AND lr.renter_user_id = sqlc.arg(renter_user_id)
  AND lr.deleted_at IS NULL
ORDER BY lr.created_at DESC;
