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
    runtime_secret_name, public_clients, public_clients_encrypted, expiry, extra,
    chart_release_name, deployment_name, service_name
) VALUES ($1, $2, $3, $4, $5, $6, $7, sqlc.arg(public_clients), $8, $9, $10, $11, $12, $13)
ON CONFLICT (id) DO UPDATE SET
    issuer_url     = EXCLUDED.issuer_url,
    cluster_id     = EXCLUDED.cluster_id,
    namespace      = EXCLUDED.namespace,
    release_name   = EXCLUDED.release_name,
    configmap_name = EXCLUDED.configmap_name,
    runtime_secret_name = EXCLUDED.runtime_secret_name,
    public_clients = CASE WHEN dex_settings.public_clients_cutover_at IS NULL THEN EXCLUDED.public_clients ELSE '[]'::jsonb END,
    public_clients_encrypted = EXCLUDED.public_clients_encrypted,
    expiry         = EXCLUDED.expiry,
    extra          = EXCLUDED.extra,
    chart_release_name = EXCLUDED.chart_release_name,
    deployment_name = EXCLUDED.deployment_name,
    service_name = EXCLUDED.service_name,
    runtime_generation = dex_settings.runtime_generation + 1,
    updated_at     = now()
RETURNING *;

-- name: MarkDexRuntimeApplied :one
UPDATE dex_settings
SET runtime_applied_generation = sqlc.arg(runtime_generation),
    updated_at = now()
WHERE id = $1
  AND runtime_generation = sqlc.arg(runtime_generation)
RETURNING *;

-- name: BackfillDexPublicClientsEnvelope :one
UPDATE dex_settings
SET public_clients_encrypted = $2,
    updated_at = now()
WHERE id = $1
  AND public_clients_cutover_at IS NULL
  AND public_clients_encrypted = ''
  AND public_clients = sqlc.arg(legacy_public_clients)::jsonb
RETURNING *;
