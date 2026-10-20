-- Vault connections CRUD (migration 067).
--
-- Canonical sqlc source for Vault connection persistence.

-- name: ListVaultConnectionsPage :many
-- List projection intentionally excludes auth_{key_id,algorithm,ciphertext,nonce}. Detail/probe paths
-- use GetVaultConnectionByID when they need to resolve credential material.
SELECT id, name, description, addr, auth_method,
       (auth_key_id <> '') AS auth_configured, namespace,
       tls_skip_verify, ca_cert_pem, default_mount, enabled,
       cached_token_expires_at, last_health_at, last_health_ok, last_error,
       created_by, created_at, updated_at
FROM vault_connections
ORDER BY name ASC, id ASC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountVaultConnections :one
SELECT count(*) FROM vault_connections;

-- name: GetVaultConnectionByID :one
SELECT *
FROM vault_connections
WHERE id = $1;

-- name: GetVaultConnectionByName :one
SELECT *
FROM vault_connections
WHERE name = $1;

-- name: CreateVaultConnection :one
INSERT INTO vault_connections (
    name, description, addr, auth_method, auth_key_id, auth_algorithm, auth_ciphertext, auth_nonce, namespace,
    tls_skip_verify, ca_cert_pem, default_mount, enabled, created_by
) VALUES ($1, $2, $3, $4, $5, $12, $13, $14, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: UpdateVaultConnection :one
UPDATE vault_connections
SET description     = $2,
    addr            = $3,
    auth_method     = $4,
    auth_key_id = $5,
    auth_algorithm = $11,
    auth_ciphertext = $12,
    auth_nonce = $13,
    namespace       = $6,
    tls_skip_verify = $7,
    ca_cert_pem     = $8,
    default_mount   = $9,
    enabled         = $10,
    updated_at      = now()
WHERE id = $1
RETURNING *;

-- name: DeleteVaultConnection :exec
DELETE FROM vault_connections WHERE id = $1;

-- name: UpdateVaultConnectionHealth :exec
UPDATE vault_connections
SET last_health_at = now(),
    last_health_ok = $2,
    last_error     = $3,
    updated_at     = now()
WHERE id = $1;

-- name: SetProjectDefaultVaultConnection :exec
UPDATE projects
SET default_vault_connection_id = $2,
    updated_at = now()
WHERE id = $1;

-- name: GetProjectDefaultVaultConnection :one
-- Returns the (possibly NULL) default_vault_connection_id pointer. Caller
-- decides whether to chase the FK; this just answers "is a default set?".
SELECT default_vault_connection_id FROM projects WHERE id = $1;
