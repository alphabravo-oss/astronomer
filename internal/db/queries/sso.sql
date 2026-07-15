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

-- All logical connector mutations stage a new runtime generation and disable
-- SSO under the same transaction-scoped advisory lock used by activation.
-- name: StageCreateDexConnector :one
WITH dex_lock AS MATERIALIZED (SELECT pg_advisory_xact_lock(742193440558879931)),
locked_settings AS MATERIALIZED (SELECT saga_previous_sso_enabled FROM dex_settings,dex_lock WHERE id='00000000-0000-0000-0000-000000000001'::uuid FOR UPDATE OF dex_settings),
bypass AS MATERIALIZED (SELECT set_config('astronomer.dex_connector_stage_bypass','1',true) FROM locked_settings),
locked_sso AS MATERIALIZED (SELECT is_enabled FROM sso_configurations,dex_lock WHERE provider='dex' FOR UPDATE OF sso_configurations),
previous_sso AS MATERIALIZED (
 SELECT COALESCE((SELECT is_enabled FROM locked_sso),false) OR COALESCE((SELECT saga_previous_sso_enabled FROM locked_settings),false) AS was_enabled FROM locked_settings
), connector AS (
 INSERT INTO dex_connectors (name,type,display_name,config,enabled)
 SELECT sqlc.arg(name),sqlc.arg(type),sqlc.arg(display_name),sqlc.arg(config),sqlc.arg(enabled) FROM bypass RETURNING *
), staged AS (
 UPDATE dex_settings SET runtime_generation=runtime_generation+1,saga_previous_sso_enabled=previous_sso.was_enabled,updated_at=now()
 FROM previous_sso,connector WHERE dex_settings.id='00000000-0000-0000-0000-000000000001'::uuid RETURNING runtime_generation
), disabled AS (
 UPDATE sso_configurations SET is_enabled=false,updated_at=now() WHERE provider='dex' AND is_enabled=true AND EXISTS(SELECT 1 FROM staged) RETURNING 1
)
SELECT connector.*,staged.runtime_generation FROM connector CROSS JOIN staged WHERE (SELECT count(*) FROM disabled)>=0;

-- name: StageUpdateDexConnector :one
WITH dex_lock AS MATERIALIZED (SELECT pg_advisory_xact_lock(742193440558879931)),
locked_settings AS MATERIALIZED (SELECT saga_previous_sso_enabled FROM dex_settings,dex_lock WHERE id='00000000-0000-0000-0000-000000000001'::uuid FOR UPDATE OF dex_settings),
locked_connector AS MATERIALIZED (SELECT dex_connectors.id FROM dex_connectors,locked_settings WHERE dex_connectors.id=sqlc.arg(connector_id) FOR UPDATE OF dex_connectors),
bypass AS MATERIALIZED (SELECT set_config('astronomer.dex_connector_stage_bypass','1',true) FROM locked_connector),
locked_sso AS MATERIALIZED (SELECT is_enabled FROM sso_configurations,dex_lock WHERE provider='dex' FOR UPDATE OF sso_configurations),
previous_sso AS MATERIALIZED (
 SELECT COALESCE((SELECT is_enabled FROM locked_sso),false) OR COALESCE((SELECT saga_previous_sso_enabled FROM locked_settings),false) AS was_enabled FROM locked_settings
), connector AS (
 UPDATE dex_connectors SET type=sqlc.arg(type),display_name=sqlc.arg(display_name),config=sqlc.arg(config),enabled=sqlc.arg(enabled),updated_at=now()
 WHERE dex_connectors.id=sqlc.arg(connector_id) AND EXISTS(SELECT 1 FROM bypass) RETURNING dex_connectors.*
), staged AS (
 UPDATE dex_settings SET runtime_generation=runtime_generation+1,saga_previous_sso_enabled=previous_sso.was_enabled,updated_at=now()
 FROM previous_sso,connector WHERE dex_settings.id='00000000-0000-0000-0000-000000000001'::uuid RETURNING runtime_generation
), disabled AS (
 UPDATE sso_configurations SET is_enabled=false,updated_at=now() WHERE provider='dex' AND is_enabled=true AND EXISTS(SELECT 1 FROM staged) RETURNING 1
)
SELECT connector.*,staged.runtime_generation FROM connector CROSS JOIN staged WHERE (SELECT count(*) FROM disabled)>=0;

-- name: StageDeleteDexConnector :one
WITH dex_lock AS MATERIALIZED (SELECT pg_advisory_xact_lock(742193440558879931)),
locked_settings AS MATERIALIZED (SELECT saga_previous_sso_enabled FROM dex_settings,dex_lock WHERE id='00000000-0000-0000-0000-000000000001'::uuid FOR UPDATE OF dex_settings),
locked_connector AS MATERIALIZED (SELECT dex_connectors.id FROM dex_connectors,locked_settings WHERE dex_connectors.id=sqlc.arg(connector_id) FOR UPDATE OF dex_connectors),
bypass AS MATERIALIZED (SELECT set_config('astronomer.dex_connector_stage_bypass','1',true) FROM locked_connector),
locked_sso AS MATERIALIZED (SELECT is_enabled FROM sso_configurations,dex_lock WHERE provider='dex' FOR UPDATE OF sso_configurations),
previous_sso AS MATERIALIZED (
 SELECT COALESCE((SELECT is_enabled FROM locked_sso),false) OR COALESCE((SELECT saga_previous_sso_enabled FROM locked_settings),false) AS was_enabled FROM locked_settings
), deleted AS (
 DELETE FROM dex_connectors WHERE dex_connectors.id=sqlc.arg(connector_id) AND EXISTS(SELECT 1 FROM bypass) RETURNING dex_connectors.id
), staged AS (
 UPDATE dex_settings SET runtime_generation=runtime_generation+1,saga_previous_sso_enabled=previous_sso.was_enabled,updated_at=now()
 FROM previous_sso,deleted WHERE dex_settings.id='00000000-0000-0000-0000-000000000001'::uuid RETURNING runtime_generation
), disabled AS (
 UPDATE sso_configurations SET is_enabled=false,updated_at=now() WHERE provider='dex' AND is_enabled=true AND EXISTS(SELECT 1 FROM staged) RETURNING 1
)
SELECT runtime_generation FROM staged WHERE (SELECT count(*) FROM disabled)>=0;

-- name: GetDexSettings :one
SELECT * FROM dex_settings WHERE id = $1;

-- name: StageDexSettingsAndDisableSSO :one
-- The previous SSO state, new settings generation, and fail-closed provider
-- disablement are one PostgreSQL statement. No process-local pre-snapshot can
-- race a concurrent stage.
WITH dex_lock AS MATERIALIZED (SELECT pg_advisory_xact_lock(742193440558879931)),
locked_settings AS MATERIALIZED (
	SELECT current_settings.saga_previous_sso_enabled FROM dex_settings AS current_settings, dex_lock
	WHERE current_settings.id=sqlc.arg(id) FOR UPDATE OF current_settings
), locked_sso AS MATERIALIZED (
	SELECT current_sso.is_enabled FROM sso_configurations AS current_sso, dex_lock
	WHERE current_sso.provider='dex' FOR UPDATE OF current_sso
), previous_sso AS MATERIALIZED (
	SELECT
		COALESCE((SELECT is_enabled FROM locked_sso), false)
		OR COALESCE((SELECT saga_previous_sso_enabled FROM locked_settings), false) AS was_enabled FROM dex_lock
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
WITH dex_lock AS MATERIALIZED (SELECT pg_advisory_xact_lock(742193440558879931)), current_generation AS MATERIALIZED (
 SELECT settings.saga_previous_sso_enabled FROM dex_settings AS settings,dex_lock
 WHERE settings.id=sqlc.arg(id) AND settings.runtime_generation=sqlc.arg(runtime_generation)
 AND settings.runtime_applied_generation=sqlc.arg(runtime_generation) AND settings.runtime_phase IN ('fresh','cutover')
 FOR UPDATE OF settings
), enabled AS (
 UPDATE sso_configurations AS sso SET is_enabled=true,updated_at=now()
 WHERE sso.provider='dex' AND EXISTS(SELECT 1 FROM current_generation WHERE saga_previous_sso_enabled=true)
 RETURNING sso.*
), cleared AS (
 UPDATE dex_settings SET saga_previous_sso_enabled=false,updated_at=now()
 WHERE id=sqlc.arg(id) AND runtime_generation=sqlc.arg(runtime_generation) AND EXISTS(SELECT 1 FROM enabled) RETURNING 1
)
SELECT enabled.* FROM enabled WHERE (SELECT count(*) FROM cleared)>0;

-- name: GetDexSettingsForGeneration :one
SELECT * FROM dex_settings
WHERE id = $1 AND runtime_generation = $2;

-- name: MarkDexRuntimeStaged :one
WITH dex_lock AS MATERIALIZED (SELECT pg_advisory_xact_lock(742193440558879931)) UPDATE dex_settings
SET runtime_staged_generation = sqlc.arg(runtime_generation),
    updated_at = now()
WHERE id = $1
  AND runtime_generation = sqlc.arg(runtime_generation)
  AND EXISTS(SELECT 1 FROM dex_lock)
RETURNING *;

-- name: MarkDexRuntimeApplied :one
WITH dex_lock AS MATERIALIZED (SELECT pg_advisory_xact_lock(742193440558879931)) UPDATE dex_settings
SET runtime_applied_generation = sqlc.arg(runtime_generation),
    updated_at = now()
WHERE id = $1
  AND runtime_generation = sqlc.arg(runtime_generation)
  AND runtime_staged_generation = sqlc.arg(runtime_generation)
  AND EXISTS(SELECT 1 FROM dex_lock)
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
