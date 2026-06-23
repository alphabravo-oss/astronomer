-- SCIM 2.0 provisioning queries (migration 114). Bearer-token auth +
-- the User/Group provisioning surface mapped onto the existing users +
-- identity_group_mappings tables.

-- name: CreateSCIMToken :one
INSERT INTO scim_tokens (name, token_hash, prefix)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetSCIMTokenByHash :one
SELECT * FROM scim_tokens WHERE token_hash = $1;

-- name: TouchSCIMToken :exec
UPDATE scim_tokens SET last_used_at = now() WHERE id = $1;

-- name: ListSCIMTokens :many
SELECT * FROM scim_tokens ORDER BY created_at DESC;

-- name: DeleteSCIMToken :exec
DELETE FROM scim_tokens WHERE id = $1;

-- name: ListSCIMGroupNames :many
-- SCIM Groups are read off the distinct group_name values an operator
-- has configured in identity_group_mappings (migration 042). Each name
-- becomes one SCIM Group resource. Paginated to match the SCIM list
-- contract.
SELECT DISTINCT group_name FROM identity_group_mappings
ORDER BY group_name
LIMIT $1 OFFSET $2;

-- name: CountSCIMGroupNames :one
SELECT count(DISTINCT group_name) FROM identity_group_mappings;
