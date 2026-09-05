-- users is a platform-global table (a person may be a renter in one org and
-- staff in another), so no org_id scoping applies here.

-- CreateUser takes the locale the signup chose (SPEC §3.2). The column
-- defaults to 'sw'; the handlers pass it explicitly so a renter who picked
-- English on the public SW/EN toggle is English from their first SMS.
-- name: CreateUser :one
INSERT INTO users (kind, phone, email, full_name, pin_hash, password_hash, email_verified_at, locale)
VALUES (
    sqlc.arg(kind),
    sqlc.narg(phone),
    sqlc.narg(email),
    sqlc.arg(full_name),
    sqlc.narg(pin_hash),
    sqlc.narg(password_hash),
    sqlc.narg(email_verified_at),
    COALESCE(sqlc.narg(locale)::text, 'sw')
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

-- SetUserEmail backs the optional `email` field of PUT /me/profile. A renter
-- registers by phone, so the address is added later or not at all.
-- name: SetUserEmail :one
UPDATE users SET email = sqlc.narg(email)
WHERE id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- SetUserLocale backs PATCH /me (renter) and PATCH /org/members/me (org user):
-- the language this person reads their screens and their SMS in.
-- name: SetUserLocale :one
UPDATE users SET locale = sqlc.arg(locale)
WHERE id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- GetUserLocale is the one-column read every queue writer makes before it
-- renders: the recipient's language, or nothing when the row is gone. It is
-- deliberately not GetUserByID — a send path should not pull password hashes
-- across the wire to learn one word.
-- name: GetUserLocale :one
SELECT locale FROM users WHERE id = sqlc.arg(id) AND deleted_at IS NULL;

-- SetUserFullName keeps the account's display name in step with the renter
-- profile: PUT /me/profile writes the name a renter types, and the landlord
-- directory reads `users.full_name` in places the profile row is not joined,
-- so leaving the two apart shows the same person under two names.
-- name: SetUserFullName :one
UPDATE users SET full_name = sqlc.arg(full_name)
WHERE id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;
