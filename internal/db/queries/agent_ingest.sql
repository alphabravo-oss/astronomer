-- Agent apiserver-audit ingest service identity (PATH A).
--
-- These power the get-or-create of the reserved service principal + its
-- cluster-scoped cluster:update grant, plus the mint-fresh-revoke-old token
-- lifecycle the tunnel CONNECT handshake drives. Kept in one file so the
-- ingest auth path is reviewable in isolation.

-- name: CreateServiceUser :one
-- Inserts a service principal (is_service=true). Service users are excluded
-- from human-user surfaces; they exist solely to satisfy the api_tokens FK and
-- carry RBAC bindings. ON CONFLICT keeps get-or-create race-safe under
-- concurrent agent connects.
INSERT INTO users (email, username, is_active, is_service)
VALUES ($1, $2, true, true)
ON CONFLICT (username) DO UPDATE SET is_service = true
RETURNING *;

-- name: GetClusterRoleByName :one
SELECT * FROM cluster_roles WHERE name = $1;

-- name: CountClusterRoleBindingForUserCluster :one
-- Whether the service user already holds the reserved role on this cluster, so
-- the connect path doesn't pile up duplicate bindings on every reconnect.
SELECT count(*) FROM cluster_role_bindings
WHERE user_id = $1 AND cluster_id = $2 AND role_id = $3;

-- name: RevokeAPITokensByName :exec
-- Revokes any prior non-revoked tokens for a service user with this name. The
-- plaintext of a previously-minted ingest token is never stored (hash-only
-- contract), so it cannot be re-delivered on reconnect; instead we revoke the
-- old row and mint a fresh one, keeping at most one valid token per cluster and
-- preventing token pileup.
UPDATE api_tokens SET is_revoked = true
WHERE user_id = $1 AND name = $2 AND is_revoked = false;
