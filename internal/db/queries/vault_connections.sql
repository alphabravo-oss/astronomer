-- Vault connections CRUD (migration 067).
--
-- Hand-edited SQL paired with the hand-authored sqlc shim in
-- internal/db/sqlc/vault_connections.sql.go (sqlc CLI not available
-- in agent worktrees; same pattern cloud_credentials uses). Keep this
-- file byte-compatible with what sqlc would emit so a future
-- `make sqlc` is a no-op.

-- name: ListVaultConnections :many
SELECT id, name, description, addr, auth_method, auth_encrypted, namespace,
       tls_skip_verify, ca_cert_pem, default_mount, enabled,
       cached_token_expires_at, last_health_at, last_health_ok, last_error,
       created_by, created_at, updated_at
FROM vault_connections
ORDER BY name ASC;

-- name: GetVaultConnectionByID :one
SELECT id, name, description, addr, auth_method, auth_encrypted, namespace,
       tls_skip_verify, ca_cert_pem, default_mount, enabled,
       cached_token_expires_at, last_health_at, last_health_ok, last_error,
       created_by, created_at, updated_at
FROM vault_connections
WHERE id = $1;

-- name: GetVaultConnectionByName :one
SELECT id, name, description, addr, auth_method, auth_encrypted, namespace,
       tls_skip_verify, ca_cert_pem, default_mount, enabled,
       cached_token_expires_at, last_health_at, last_health_ok, last_error,
       created_by, created_at, updated_at
FROM vault_connections
WHERE name = $1;

-- name: CreateVaultConnection :one
INSERT INTO vault_connections (
    name, description, addr, auth_method, auth_encrypted, namespace,
    tls_skip_verify, ca_cert_pem, default_mount, enabled, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING id, name, description, addr, auth_method, auth_encrypted, namespace,
          tls_skip_verify, ca_cert_pem, default_mount, enabled,
          cached_token_expires_at, last_health_at, last_health_ok, last_error,
          created_by, created_at, updated_at;

-- name: UpdateVaultConnection :one
UPDATE vault_connections
SET description     = $2,
    addr            = $3,
    auth_method     = $4,
    auth_encrypted  = $5,
    namespace       = $6,
    tls_skip_verify = $7,
    ca_cert_pem     = $8,
    default_mount   = $9,
    enabled         = $10,
    updated_at      = now()
WHERE id = $1
RETURNING id, name, description, addr, auth_method, auth_encrypted, namespace,
          tls_skip_verify, ca_cert_pem, default_mount, enabled,
          cached_token_expires_at, last_health_at, last_health_ok, last_error,
          created_by, created_at, updated_at;

-- name: DeleteVaultConnection :exec
DELETE FROM vault_connections WHERE id = $1;

-- name: UpdateVaultConnectionHealth :exec
UPDATE vault_connections
SET last_health_at = now(),
    last_health_ok = $2,
    last_error     = $3,
    updated_at     = now()
WHERE id = $1;

-- name: UpdateVaultConnectionTokenExpiry :exec
UPDATE vault_connections
SET cached_token_expires_at = $2,
    updated_at = now()
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
