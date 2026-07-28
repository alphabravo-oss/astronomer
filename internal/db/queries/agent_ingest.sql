-- Agent apiserver-audit ingest service identity (PATH A).
--
-- These power the get-or-create of the per-cluster service principal + its
-- cluster-scoped audit_ingest:create grant, plus the mint-fresh-revoke-old token
-- lifecycle the tunnel CONNECT handshake drives. Kept in one file so the
-- ingest auth path is reviewable in isolation.

-- name: CreateServiceUser :one
-- Inserts a service principal (is_service=true). Service users are excluded
-- from human-user surfaces; they exist solely to satisfy the api_tokens FK and
-- carry RBAC bindings. ON CONFLICT keeps get-or-create race-safe under
-- concurrent agent connects, and re-asserts is_active: a decommission (or an
-- operator/SCIM reconcile) that deactivated the row would otherwise wedge
-- ingest forever, since auth rejects tokens whose owner is inactive.
INSERT INTO users (email, username, is_active, is_service)
VALUES ($1, $2, true, true)
ON CONFLICT (username) DO UPDATE SET is_service = true, is_active = true
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

-- name: RevokeAgentIngestIdentityForCluster :execrows
-- Retires a decommissioned cluster's ingest identity: revoke its token and
-- deactivate the per-cluster service principal that owns it. Decommission only
-- tombstones the cluster row, so no FK cascade fires — without this the
-- identity survives forever as a live bearer credential with zero RBAC
-- bindings (its cluster_role_binding is deleted by the same phase). The
-- data-modifying CTE always runs to completion; the returned count is the
-- number of tokens revoked. $1 is the service username, $2 the token name.
WITH deactivated AS (
    UPDATE users SET is_active = false, updated_at = now()
    WHERE username = $1 AND is_service = true AND is_active = true
    RETURNING id
)
UPDATE api_tokens SET is_revoked = true
WHERE name = $2 AND is_revoked = false;
