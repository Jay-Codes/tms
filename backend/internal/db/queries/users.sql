-- users is a platform-global table (a person may be a renter in one org and
-- staff in another), so no org_id scoping applies here.

-- name: CreateUser :one
INSERT INTO users (kind, phone, email, full_name, pin_hash, password_hash, email_verified_at)
VALUES (
    sqlc.arg(kind),
    sqlc.narg(phone),
    sqlc.narg(email),
    sqlc.arg(full_name),
    sqlc.narg(pin_hash),
    sqlc.narg(password_hash),
    sqlc.narg(email_verified_at)
)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = sqlc.arg(id) AND deleted_at IS NULL;

-- name: GetUserByPhone :one
SELECT * FROM users WHERE phone = sqlc.arg(phone) AND deleted_at IS NULL;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE lower(email) = lower(sqlc.arg(email)) AND deleted_at IS NULL;

-- name: SetUserPin :exec
UPDATE users SET pin_hash = sqlc.arg(pin_hash) WHERE id = sqlc.arg(id);

-- name: SetUserPassword :exec
UPDATE users SET password_hash = sqlc.arg(password_hash) WHERE id = sqlc.arg(id);

-- name: MarkEmailVerified :one
UPDATE users SET email_verified_at = COALESCE(email_verified_at, now())
WHERE id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- name: CountPlatformAdmins :one
SELECT count(*) FROM users WHERE kind = 'platform_admin' AND deleted_at IS NULL;
