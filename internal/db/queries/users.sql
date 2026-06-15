-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = $1;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: CreateUser :one
INSERT INTO users (email, username, first_name, last_name, password, is_active, is_staff, is_superuser)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: CreateBootstrapAdmin :one
-- Creates the initial admin user that ensure_admin runs on first boot of a
-- fresh database. The password is either operator-provided through Helm values
-- or auto-generated into the bootstrap Secret; the account is immediately
-- usable and is not forced through a first-login password reset.
INSERT INTO users (email, username, first_name, last_name, password, is_active, is_staff, is_superuser)
VALUES ($1, $2, $3, $4, $5, true, true, true)
RETURNING *;

-- name: ClearMustChangePassword :exec
UPDATE users SET must_change_password = false, updated_at = now() WHERE id = $1;

-- name: UpdateUser :one
UPDATE users SET
    email = $2,
    username = $3,
    first_name = $4,
    last_name = $5,
    is_active = $6,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateUserPassword :exec
UPDATE users SET password = $2, updated_at = now() WHERE id = $1;

-- name: UpdateUserPasswordHash :exec
-- Convenience alias used by the login flow when an inherited Django
-- PBKDF2/argon2 hash is upgraded to bcrypt on first successful match.
UPDATE users SET password = $2, updated_at = now() WHERE id = $1;

-- name: UpdateUserLastLogin :exec
UPDATE users SET last_login = now() WHERE id = $1;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;
