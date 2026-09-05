-- Properties are org-scoped: every statement filters by org_id, so a property
-- belonging to another org is indistinguishable from one that does not exist
-- (the handler turns "no rows" into 404 — API.md).

-- name: CreateProperty :one
INSERT INTO properties (org_id, name, location_text, lat, lng, notes)
VALUES (
    sqlc.arg(org_id),
    sqlc.arg(name),
    sqlc.arg(location_text),
    sqlc.narg(lat),
    sqlc.narg(lng),
    sqlc.narg(notes)
)
RETURNING *;

-- name: ListProperties :many
SELECT p.id, p.org_id, p.name, p.location_text, p.lat, p.lng, p.notes,
       p.created_at, p.updated_at,
       c.total, c.vacant, c.occupied, c.maintenance, c.unlisted
FROM properties p
LEFT JOIN LATERAL (
    SELECT count(*)                                            AS total,
           count(*) FILTER (WHERE u.status = 'vacant')         AS vacant,
           count(*) FILTER (WHERE u.status = 'occupied')       AS occupied,
           count(*) FILTER (WHERE u.status = 'maintenance')    AS maintenance,
           count(*) FILTER (WHERE u.status = 'unlisted')       AS unlisted
    FROM units u
    WHERE u.property_id = p.id AND u.org_id = p.org_id AND u.deleted_at IS NULL
) c ON true
WHERE p.org_id = sqlc.arg(org_id) AND p.deleted_at IS NULL
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (p.created_at, p.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY p.created_at DESC, p.id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetProperty :one
SELECT p.id, p.org_id, p.name, p.location_text, p.lat, p.lng, p.notes,
       p.created_at, p.updated_at,
       c.total, c.vacant, c.occupied, c.maintenance, c.unlisted
FROM properties p
LEFT JOIN LATERAL (
    SELECT count(*)                                            AS total,
           count(*) FILTER (WHERE u.status = 'vacant')         AS vacant,
           count(*) FILTER (WHERE u.status = 'occupied')       AS occupied,
           count(*) FILTER (WHERE u.status = 'maintenance')    AS maintenance,
           count(*) FILTER (WHERE u.status = 'unlisted')       AS unlisted
    FROM units u
    WHERE u.property_id = p.id AND u.org_id = p.org_id AND u.deleted_at IS NULL
) c ON true
WHERE p.org_id = sqlc.arg(org_id) AND p.id = sqlc.arg(id) AND p.deleted_at IS NULL;

-- name: UpdateProperty :one
UPDATE properties
SET name          = COALESCE(sqlc.narg(name), name),
    location_text = COALESCE(sqlc.narg(location_text), location_text),
    lat           = CASE WHEN sqlc.arg(set_lat)::boolean THEN sqlc.narg(lat)::double precision ELSE lat END,
    lng           = CASE WHEN sqlc.arg(set_lng)::boolean THEN sqlc.narg(lng)::double precision ELSE lng END,
    notes         = CASE WHEN sqlc.arg(set_notes)::boolean THEN sqlc.narg(notes)::text ELSE notes END
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteProperty :one
UPDATE properties SET deleted_at = now()
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteUnitsOfProperty :exec
UPDATE units SET deleted_at = now()
WHERE org_id = sqlc.arg(org_id) AND property_id = sqlc.arg(property_id) AND deleted_at IS NULL;

-- name: CountBlockingContractsForProperty :one
SELECT count(*)
FROM contracts c
JOIN units u ON u.id = c.unit_id
WHERE c.org_id = sqlc.arg(org_id)
  AND u.property_id = sqlc.arg(property_id)
  AND c.status IN ('active', 'pending_signature')
  AND c.deleted_at IS NULL;
