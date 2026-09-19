-- Phase 16 §16.2: CSV import batches and their rows.
--
-- Everything here is org-scoped: a batch belongs to the org whose landlord
-- uploaded it, and a row is reached only through its batch's org. The commit
-- and undo statements additionally pin the status they may move from, so two
-- clicks on "Commit" cannot both write.

-- name: CreateImportBatch :one
INSERT INTO import_batches (
    org_id, kind, filename, row_count, ok_count, error_count, created_by_user_id
)
VALUES (
    sqlc.arg(org_id), sqlc.arg(kind), sqlc.arg(filename),
    sqlc.arg(row_count), sqlc.arg(ok_count), sqlc.arg(error_count),
    sqlc.narg(created_by_user_id)
)
RETURNING *;

-- name: CreateImportRow :one
INSERT INTO import_rows (batch_id, org_id, line, raw, errors, resolved)
VALUES (
    sqlc.arg(batch_id), sqlc.arg(org_id), sqlc.arg(line),
    sqlc.arg(raw), sqlc.narg(errors), sqlc.narg(resolved)
)
RETURNING *;

-- name: GetImportBatch :one
SELECT b.*, u.full_name AS created_by_name
FROM import_batches b
LEFT JOIN users u ON u.id = b.created_by_user_id
WHERE b.org_id = sqlc.arg(org_id) AND b.id = sqlc.arg(id) AND b.deleted_at IS NULL;

-- LockImportBatch is the commit/undo guard: the batch is read and locked inside
-- the movement's own transaction, so a second click finds the status it already
-- moved to rather than writing the spreadsheet twice.
-- name: LockImportBatch :one
SELECT * FROM import_batches
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
FOR UPDATE;

-- name: ListImportBatches :many
SELECT b.*, u.full_name AS created_by_name
FROM import_batches b
LEFT JOIN users u ON u.id = b.created_by_user_id
WHERE b.org_id = sqlc.arg(org_id) AND b.deleted_at IS NULL
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (b.created_at, b.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY b.created_at DESC, b.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListImportRows :many
SELECT * FROM import_rows
WHERE org_id = sqlc.arg(org_id) AND batch_id = sqlc.arg(batch_id)
ORDER BY line;

-- MarkImportRowEntity stamps a committed row with what it became. `resolved` is
-- rewritten at the same time so it carries the ids the commit actually created
-- (the property a units row also had to make, the contract a renters row drew
-- up) — which is what the undo walks 24 h later.
-- name: MarkImportRowEntity :exec
UPDATE import_rows
SET entity_type = sqlc.narg(entity_type),
    entity_id   = sqlc.narg(entity_id),
    resolved    = COALESCE(sqlc.narg(resolved), resolved)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id);

-- name: CommitImportBatch :one
UPDATE import_batches
SET status = 'committed', committed_at = now()
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
  AND status = 'previewed' AND deleted_at IS NULL
RETURNING *;

-- name: UndoImportBatch :one
UPDATE import_batches
SET status = 'undone', undone_at = now()
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
  AND status = 'committed' AND deleted_at IS NULL
RETURNING *;

-- CountImportPreviewsSince backs the "20 previews an hour" ceiling when Redis
-- is unavailable and the limiter fails open.
-- name: CountImportPreviewsSince :one
SELECT count(*) FROM import_batches
WHERE org_id = sqlc.arg(org_id) AND created_at >= sqlc.arg(since);

-- ----------------------------------------------------------- resolution --

-- FindPropertyByName matches the `property` column of a units or renters sheet.
-- Names are compared case-insensitively and trimmed, which is what a landlord
-- typing "block a" beside "Block A" means.
-- name: FindPropertyByName :many
SELECT * FROM properties
WHERE org_id = sqlc.arg(org_id) AND deleted_at IS NULL
  AND lower(btrim(name)) = lower(btrim(sqlc.arg(name)::text))
ORDER BY created_at
LIMIT 2;

-- name: FindUnitByNameInProperty :many
SELECT * FROM units
WHERE org_id = sqlc.arg(org_id) AND property_id = sqlc.arg(property_id)
  AND deleted_at IS NULL
  AND lower(btrim(name)) = lower(btrim(sqlc.arg(name)::text))
ORDER BY created_at
LIMIT 2;

-- FindUnitByNameInOrg resolves the `unit` column of a payments sheet, which
-- carries no property. Two units of the same name in different properties make
-- the row ambiguous rather than arbitrary, so the LIMIT 2 is deliberate.
-- name: FindUnitByNameInOrg :many
SELECT u.*, p.name AS property_name
FROM units u
JOIN properties p ON p.id = u.property_id AND p.org_id = u.org_id
WHERE u.org_id = sqlc.arg(org_id) AND u.deleted_at IS NULL AND p.deleted_at IS NULL
  AND lower(btrim(u.name)) = lower(btrim(sqlc.arg(name)::text))
ORDER BY u.created_at
LIMIT 2;

-- FindContractForUnitAndRenter resolves a payments row to the contract the
-- money belongs to: the renter's tenancy on that unit, newest first. History
-- may belong to a finished contract, so `ended` and `terminated` count
-- (PLAN2 §16.2).
-- name: FindContractForUnitAndRenter :many
SELECT * FROM contracts
WHERE org_id = sqlc.arg(org_id) AND unit_id = sqlc.arg(unit_id)
  AND renter_user_id = sqlc.arg(renter_user_id) AND deleted_at IS NULL
  AND status IN ('active', 'expiring', 'ended', 'terminated')
ORDER BY start_date DESC, created_at DESC
LIMIT 1;

-- ----------------------------------------------------------------- undo --

-- ListImportedPayments is the undo's worklist: every live payment that arrived
-- on this batch, oldest first so the reversals unwind in the order the money
-- was applied.
-- name: ListImportedPayments :many
SELECT * FROM payments
WHERE org_id = sqlc.arg(org_id) AND import_batch_id = sqlc.arg(import_batch_id)
  AND status = 'recorded' AND deleted_at IS NULL
ORDER BY paid_at, id;

-- CountPaymentsForContract says whether a contract created by an import has
-- been used since: money against it makes it "touched", and an undo leaves it
-- alone.
-- name: CountPaymentsForContract :one
SELECT count(*) FROM payments
WHERE org_id = sqlc.arg(org_id) AND contract_id = sqlc.arg(contract_id)
  AND deleted_at IS NULL;

-- WithdrawImportedContract soft-deletes a contract an import created, and only
-- while it is still unsigned: an activated tenancy is never undone by an import
-- rollback (it is terminated through the ordinary path instead).
-- name: WithdrawImportedContract :one
UPDATE contracts SET deleted_at = now()
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
  AND status = 'pending_signature' AND deleted_at IS NULL
RETURNING *;

-- name: WithdrawImportedLinkRequest :exec
UPDATE unit_link_requests SET status = 'cancelled', deleted_at = now()
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL;

-- CountLiveUnitsForProperty decides whether a property an import created is
-- still empty after its units have been taken back.
-- name: CountLiveUnitsForProperty :one
SELECT count(*) FROM units
WHERE org_id = sqlc.arg(org_id) AND property_id = sqlc.arg(property_id)
  AND deleted_at IS NULL;

-- CountContractsForUnit counts every contract ever written against a unit, in
-- any status: one is enough to call the unit touched.
-- name: CountContractsForUnit :one
SELECT count(*) FROM contracts
WHERE org_id = sqlc.arg(org_id) AND unit_id = sqlc.arg(unit_id) AND deleted_at IS NULL;

-- CountRenterReferencesAnywhere is the question that actually decides whether a
-- renter account may be taken back. `users` is platform-global, so an org-scoped
-- count is the wrong instrument: org A's undo must not delete the account org B
-- has since given a tenancy or a link request to. Everything that makes a person
-- real to somebody is counted here — contracts, link requests and staff
-- memberships — across every org.
-- guard-exempt: deliberately cross-org. The account being tested lives in the platform-global `users` table, and the whole point of the count is to see the orgs the caller cannot: a per-org answer would authorise deleting another landlord's renter.
-- name: CountRenterReferencesAnywhere :one
SELECT (
    (SELECT count(*) FROM contracts c
      WHERE c.renter_user_id = sqlc.arg(user_id) AND c.deleted_at IS NULL)
  + (SELECT count(*) FROM unit_link_requests lr
      WHERE lr.renter_user_id = sqlc.arg(user_id) AND lr.deleted_at IS NULL)
  + (SELECT count(*) FROM org_members m
      WHERE m.user_id = sqlc.arg(user_id) AND m.deleted_at IS NULL)
)::bigint AS refs;

-- SoftDeleteImportedRenter takes back an account the import itself created, and
-- only one that never became a real login: `pin_hash IS NULL` means nobody has
-- ever signed in as this person.
-- guard-exempt: users is a platform-global table with no org_id; the caller has already proved, with CountRenterReferencesAnywhere, that this org's batch created the account and that no org anywhere still references it.
-- name: SoftDeleteImportedRenter :exec
UPDATE users SET deleted_at = now()
WHERE id = sqlc.arg(id) AND kind = 'renter' AND pin_hash IS NULL
  AND password_hash IS NULL AND deleted_at IS NULL;

-- ListUnitNamesForProperty loads a property's unit names in one round trip, so
-- a 5 000-row units sheet checks for duplicates in memory rather than with one
-- query per line.
-- name: ListUnitNamesForProperty :many
SELECT lower(btrim(name))::text AS name
FROM units
WHERE org_id = sqlc.arg(org_id) AND property_id = sqlc.arg(property_id)
  AND deleted_at IS NULL;
