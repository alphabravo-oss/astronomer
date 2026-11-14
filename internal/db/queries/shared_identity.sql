-- name: ResolveSharedIdentityUser :one
SELECT u.* FROM users u
JOIN shared_identity_bindings b ON b.user_id = u.id
WHERE b.issuer = $1 AND b.subject = $2
  AND u.is_active = true AND u.is_service = false;

-- name: CreateSharedIdentityBinding :one
-- Caller must authorize and audit this mutation in the same transaction.
-- A conflict is an error: never silently transfer an existing external identity.
INSERT INTO shared_identity_bindings (issuer, subject, user_id, linked_by)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: DeleteSharedIdentityBinding :execrows
-- Caller must authorize and audit this mutation in the same transaction.
DELETE FROM shared_identity_bindings WHERE issuer = $1 AND subject = $2 AND user_id = $3;

-- name: ListSharedIdentityBindings :many
SELECT * FROM shared_identity_bindings WHERE issuer = $1
ORDER BY created_at, subject LIMIT $2 OFFSET $3;
