-- SCIM 2.0 provisioning queries. Bearer-token auth +
-- the User/Group provisioning surface mapped onto the existing users +
-- identity_group_mappings tables.

-- name: CreateSCIMToken :one
INSERT INTO scim_tokens (name, token_hash, prefix, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetSCIMTokenByHash :one
SELECT * FROM scim_tokens
WHERE token_hash = $1
  AND revoked_at IS NULL
  AND expires_at > now();

-- name: TouchSCIMToken :exec
UPDATE scim_tokens SET last_used_at = now() WHERE id = $1;

-- name: ListSCIMTokenMetadata :many
-- Never fetch token_hash for an operator list view.
SELECT id, name, prefix, last_used_at, expires_at, revoked_at, created_at
FROM scim_tokens
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountSCIMTokens :one
SELECT count(*) FROM scim_tokens;

-- name: SCIMGroupExists :one
SELECT EXISTS (
    SELECT 1 FROM identity_group_mappings WHERE group_name = $1
);

-- name: RevokeSCIMToken :execrows
UPDATE scim_tokens
SET revoked_at = now()
WHERE id = $1 AND revoked_at IS NULL;

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
