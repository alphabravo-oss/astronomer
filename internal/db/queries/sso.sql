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

-- name: StageDexSettingsAndDisableSSO :one
-- The previous SSO state, new settings generation, and fail-closed provider
-- disablement are one PostgreSQL statement. No process-local pre-snapshot can
-- race a concurrent stage.
WITH previous_sso AS MATERIALIZED (
	SELECT
		COALESCE((SELECT is_enabled FROM sso_configurations WHERE provider = 'dex'), false)
		OR COALESCE((
			SELECT saga_previous_sso_enabled
			FROM dex_settings AS current_settings
			WHERE current_settings.id = sqlc.arg(id)
		), false) AS was_enabled
), staged AS (
    INSERT INTO dex_settings (
        id, issuer_url, cluster_id, namespace, release_name, configmap_name,
        runtime_secret_name, public_clients, public_clients_encrypted, expiry, extra,
        chart_release_name, deployment_name, service_name, runtime_phase,
        saga_previous_sso_enabled
    )
    SELECT
        sqlc.arg(id), sqlc.arg(issuer_url), sqlc.arg(cluster_id), sqlc.arg(namespace),
        sqlc.arg(release_name), sqlc.arg(configmap_name), sqlc.arg(runtime_secret_name),
        sqlc.arg(public_clients), sqlc.arg(public_clients_encrypted), sqlc.arg(expiry),
        sqlc.arg(extra), sqlc.arg(chart_release_name), sqlc.arg(deployment_name),
        sqlc.arg(service_name), sqlc.arg(runtime_phase), previous_sso.was_enabled
    FROM previous_sso
    ON CONFLICT (id) DO UPDATE SET
        issuer_url = EXCLUDED.issuer_url,
        cluster_id = EXCLUDED.cluster_id,
        namespace = EXCLUDED.namespace,
        release_name = EXCLUDED.release_name,
        configmap_name = EXCLUDED.configmap_name,
        runtime_secret_name = EXCLUDED.runtime_secret_name,
        public_clients = CASE WHEN dex_settings.public_clients_cutover_at IS NULL THEN EXCLUDED.public_clients ELSE '[]'::jsonb END,
        public_clients_encrypted = EXCLUDED.public_clients_encrypted,
        expiry = EXCLUDED.expiry,
        extra = EXCLUDED.extra,
        chart_release_name = EXCLUDED.chart_release_name,
        deployment_name = EXCLUDED.deployment_name,
        service_name = EXCLUDED.service_name,
        runtime_phase = EXCLUDED.runtime_phase,
        saga_previous_sso_enabled = EXCLUDED.saga_previous_sso_enabled,
        runtime_generation = dex_settings.runtime_generation + 1,
        updated_at = now()
    RETURNING *
), disabled AS (
    UPDATE sso_configurations
    SET is_enabled = false, updated_at = now()
    WHERE provider = 'dex' AND is_enabled = true
    RETURNING 1
)
SELECT staged.runtime_generation
FROM staged
WHERE (SELECT count(*) FROM disabled) >= 0;

-- name: RestoreDexSSOForGeneration :one
UPDATE sso_configurations AS sso
SET is_enabled = true, updated_at = now()
WHERE sso.provider = 'dex'
  AND EXISTS (
      SELECT 1 FROM dex_settings AS settings
      WHERE settings.id = sqlc.arg(id)
        AND settings.runtime_generation = sqlc.arg(runtime_generation)
        AND settings.saga_previous_sso_enabled = true
  )
RETURNING sso.*;

-- name: GetDexSettingsForGeneration :one
SELECT * FROM dex_settings
WHERE id = $1 AND runtime_generation = $2;

-- name: MarkDexRuntimeStaged :one
UPDATE dex_settings
SET runtime_staged_generation = sqlc.arg(runtime_generation),
    updated_at = now()
WHERE id = $1
  AND runtime_generation = sqlc.arg(runtime_generation)
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
