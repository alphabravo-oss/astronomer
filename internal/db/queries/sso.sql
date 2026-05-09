-- Phase B4: Dex shim CRUD.
-- The Dex install itself is just a normal cluster_tools row (see migration
-- 023). Sensitive fields inside `config` are encrypted by the handler before
-- they ever land here; sqlc passes the JSONB through unchanged.

-- name: GetDexConnectorByID :one
SELECT * FROM dex_connectors WHERE id = $1;

-- name: GetDexConnectorByName :one
SELECT * FROM dex_connectors WHERE name = $1;

-- name: ListDexConnectors :many
SELECT * FROM dex_connectors ORDER BY name ASC;

-- name: ListEnabledDexConnectors :many
SELECT * FROM dex_connectors WHERE enabled = true ORDER BY name ASC;

-- name: CreateDexConnector :one
INSERT INTO dex_connectors (name, type, display_name, config, enabled)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateDexConnector :one
UPDATE dex_connectors SET
    type         = $2,
    display_name = $3,
    config       = $4,
    enabled      = $5,
    updated_at   = now()
WHERE id = $1
RETURNING *;

-- name: DeleteDexConnector :exec
DELETE FROM dex_connectors WHERE id = $1;

-- name: GetDexSettings :one
SELECT * FROM dex_settings WHERE id = $1;

-- name: UpsertDexSettings :one
INSERT INTO dex_settings (
    id, issuer_url, cluster_id, namespace, release_name, configmap_name,
    public_clients, expiry, extra
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE SET
    issuer_url     = EXCLUDED.issuer_url,
    cluster_id     = EXCLUDED.cluster_id,
    namespace      = EXCLUDED.namespace,
    release_name   = EXCLUDED.release_name,
    configmap_name = EXCLUDED.configmap_name,
    public_clients = EXCLUDED.public_clients,
    expiry         = EXCLUDED.expiry,
    extra          = EXCLUDED.extra,
    updated_at     = now()
RETURNING *;
