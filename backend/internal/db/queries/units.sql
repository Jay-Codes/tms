-- Units carry the public `unit_code` printed on the QR sticker. Every
-- landlord-facing statement is org-scoped; the single public lookup is
-- explicitly exempt (it is keyed by the unguessable code, which IS the
-- capability, and the row it returns is the org's own public data).
--
-- The row shape shared by the list/get endpoints resolves, per unit:
--   * property_name        — the vacancy board shows it without a second call
--   * the current price    — newest price_plan with effective_from <= today
--   * vacant_since         — for vacant units: the later of the unit's own
--                            creation and the end of its last finished
--                            contract (contracts is empty until Phase 4; the
--                            expression already accounts for it).

-- name: CreateUnit :one
INSERT INTO units (org_id, property_id, name, unit_code, allowed_period_ids)
VALUES (
    sqlc.arg(org_id),
    sqlc.arg(property_id),
    sqlc.arg(name),
    sqlc.arg(unit_code),
    sqlc.narg(allowed_period_ids)
)
RETURNING *;

-- name: ListUnits :many
SELECT u.id, u.org_id, u.property_id, u.name, u.unit_code, u.status,
       u.status_override, u.allowed_period_ids, u.created_at, u.updated_at,
       p.name AS property_name,
       pp.id AS price_id,
       COALESCE(pp.amount, 0)::bigint      AS price_amount,
       COALESCE(pp.currency, 'TZS')::text  AS price_currency,
       COALESCE(pp.period_days, 0)::int    AS price_period_days,
       pp.effective_from AS price_effective_from,
       (CASE WHEN u.status = 'vacant'
             THEN GREATEST(u.created_at, lc.last_end::timestamptz)
             ELSE NULL END)::timestamptz AS vacant_since
FROM units u
JOIN properties p ON p.id = u.property_id AND p.org_id = u.org_id
LEFT JOIN LATERAL (
    SELECT pl.id, pl.amount, pl.currency, pl.period_days, pl.effective_from
    FROM price_plans pl
    WHERE pl.unit_id = u.id AND pl.org_id = u.org_id AND pl.deleted_at IS NULL
      AND pl.effective_from <= CURRENT_DATE
    ORDER BY pl.effective_from DESC, pl.created_at DESC
    LIMIT 1
) pp ON true
LEFT JOIN LATERAL (
    SELECT max(c.end_date) AS last_end
    FROM contracts c
    WHERE c.unit_id = u.id AND c.org_id = u.org_id AND c.deleted_at IS NULL
      AND c.status IN ('ended', 'terminated')
) lc ON true
WHERE u.org_id = sqlc.arg(org_id) AND u.deleted_at IS NULL
  AND (sqlc.narg(status)::text IS NULL OR u.status = sqlc.narg(status)::text)
  AND (sqlc.narg(property_id)::uuid IS NULL OR u.property_id = sqlc.narg(property_id)::uuid)
  AND (sqlc.narg(q)::text IS NULL
       OR u.name ILIKE '%' || sqlc.narg(q)::text || '%'
       OR p.name ILIKE '%' || sqlc.narg(q)::text || '%')
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (u.created_at, u.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY u.created_at DESC, u.id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetUnit :one
SELECT u.id, u.org_id, u.property_id, u.name, u.unit_code, u.status,
       u.status_override, u.allowed_period_ids, u.created_at, u.updated_at,
       p.name AS property_name,
       pp.id AS price_id,
       COALESCE(pp.amount, 0)::bigint      AS price_amount,
       COALESCE(pp.currency, 'TZS')::text  AS price_currency,
       COALESCE(pp.period_days, 0)::int    AS price_period_days,
       pp.effective_from AS price_effective_from,
       (CASE WHEN u.status = 'vacant'
             THEN GREATEST(u.created_at, lc.last_end::timestamptz)
             ELSE NULL END)::timestamptz AS vacant_since
FROM units u
JOIN properties p ON p.id = u.property_id AND p.org_id = u.org_id
LEFT JOIN LATERAL (
    SELECT pl.id, pl.amount, pl.currency, pl.period_days, pl.effective_from
    FROM price_plans pl
    WHERE pl.unit_id = u.id AND pl.org_id = u.org_id AND pl.deleted_at IS NULL
      AND pl.effective_from <= CURRENT_DATE
    ORDER BY pl.effective_from DESC, pl.created_at DESC
    LIMIT 1
) pp ON true
LEFT JOIN LATERAL (
    SELECT max(c.end_date) AS last_end
    FROM contracts c
    WHERE c.unit_id = u.id AND c.org_id = u.org_id AND c.deleted_at IS NULL
      AND c.status IN ('ended', 'terminated')
) lc ON true
WHERE u.org_id = sqlc.arg(org_id) AND u.id = sqlc.arg(id) AND u.deleted_at IS NULL;

-- name: UpdateUnit :one
UPDATE units
SET name               = COALESCE(sqlc.narg(name), name),
    status             = COALESCE(sqlc.narg(status), status),
    status_override    = COALESCE(sqlc.narg(status_override), status_override),
    allowed_period_ids = CASE WHEN sqlc.arg(set_allowed)::boolean
                              THEN sqlc.narg(allowed_period_ids)::uuid[]
                              ELSE allowed_period_ids END
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteUnit :one
UPDATE units SET deleted_at = now()
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- name: CountBlockingContractsForUnit :one
SELECT count(*) FROM contracts c
WHERE c.org_id = sqlc.arg(org_id) AND c.unit_id = sqlc.arg(unit_id)
  AND c.status IN ('active', 'pending_signature') AND c.deleted_at IS NULL;

-- guard-exempt: uniqueness of unit_code is global (the code is the public
-- capability in a QR sticker), so the collision check cannot be org-scoped.
-- name: UnitCodeTaken :one
SELECT EXISTS (SELECT 1 FROM units WHERE unit_code = sqlc.arg(unit_code));

-- guard-exempt: public QR resolution (GET /public/units/{unit_code}). The
-- caller has no session and no org; the unguessable code is the capability,
-- and the org it belongs to is returned rather than supplied.
-- name: GetUnitByCode :one
SELECT u.id, u.org_id, u.property_id, u.name, u.unit_code, u.status,
       u.allowed_period_ids,
       p.name AS property_name, p.location_text AS property_location_text,
       pp.id AS price_id,
       COALESCE(pp.amount, 0)::bigint     AS price_amount,
       COALESCE(pp.currency, 'TZS')::text AS price_currency,
       COALESCE(pp.period_days, 0)::int   AS price_period_days
FROM units u
JOIN properties p ON p.id = u.property_id
LEFT JOIN LATERAL (
    SELECT pl.id, pl.amount, pl.currency, pl.period_days
    FROM price_plans pl
    WHERE pl.unit_id = u.id AND pl.org_id = u.org_id AND pl.deleted_at IS NULL
      AND pl.effective_from <= CURRENT_DATE
    ORDER BY pl.effective_from DESC, pl.created_at DESC
    LIMIT 1
) pp ON true
WHERE u.unit_code = sqlc.arg(unit_code)
  AND u.deleted_at IS NULL AND p.deleted_at IS NULL;
