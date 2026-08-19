-- name: GetDefaultMonitoringBackend :one
SELECT * FROM monitoring_backends
WHERE is_default = true OR name = 'default'
ORDER BY is_default DESC, created_at ASC
LIMIT 1;

-- name: UpsertDefaultMonitoringBackend :one
-- auth_config and auth_config_encrypted are two halves of ONE value (migration
-- 146) and this statement always writes both. Every caller builds its params
-- through handler.sealMonitoringBackendAuthConfig / tasks.sealMonitoringBackendAuthConfig,
-- which set the pair together from a RESOLVED document — a writer that set the
-- projection without the envelope would silently drop the credential during an
-- unrelated config edit.
WITH clear_default AS (
    UPDATE monitoring_backends SET is_default = false WHERE is_default = true AND name <> 'default'
)
INSERT INTO monitoring_backends (
    name,
    backend_type,
    query_url,
    alertmanager_url,
    tenant_id,
    auth_type,
    auth_config,
    auth_config_encrypted,
    default_step_seconds,
    timeout_seconds,
    is_default,
    created_by_id
)
VALUES ('default', $1, $2, $3, $4, $5, $6, $7, $8, $9, true, $10)
ON CONFLICT (name) DO UPDATE SET
    backend_type = EXCLUDED.backend_type,
    query_url = EXCLUDED.query_url,
    alertmanager_url = EXCLUDED.alertmanager_url,
    tenant_id = EXCLUDED.tenant_id,
    auth_type = EXCLUDED.auth_type,
    auth_config = EXCLUDED.auth_config,
    auth_config_encrypted = EXCLUDED.auth_config_encrypted,
    default_step_seconds = EXCLUDED.default_step_seconds,
    timeout_seconds = EXCLUDED.timeout_seconds,
    is_default = true,
    updated_at = now()
RETURNING *;

-- name: ListMonitoringBackendsWithLegacyAuthConfig :many
-- Rows that still hold their credential as plaintext JSONB, for the
-- security:migrate_plaintext_credentials sweep. Empty ciphertext is the only
-- marker of a pre-146 row.
--
-- The EXISTS clause is what makes "returned by this query" mean "has something
-- to seal", and it must stay in exact step with monitoring.HasAuthConfigSecret:
-- at least one key OUTSIDE the non-secret allow-list. A looser predicate
-- (auth_config <> '{}') also matches rows the sweep can never seal — a backend
-- whose auth_config is nothing but {"operationPolicies":{...}}, which is the
-- common case for an unauthenticated in-cluster Prometheus. Those rows never
-- leave the result set, so a full page of them ahead of a credentialed row
-- would make the sweep re-read the same page forever and never reach it.
--
-- jsonb_typeof guards jsonb_object_keys, which errors on a non-object.
SELECT * FROM monitoring_backends
WHERE auth_config_encrypted = ''
  AND auth_config IS NOT NULL
  AND jsonb_typeof(auth_config) = 'object'
  AND EXISTS (
        SELECT 1
        FROM jsonb_object_keys(auth_config) AS config_key
        WHERE config_key <> ALL (ARRAY[
            'operationPolicies', 'sharedThanos', 'sharedAlertmanager',
            'sharedGrafana', 'sharedAlertingAssets', 'status'
        ])
      )
ORDER BY id
LIMIT $1 OFFSET $2;

-- name: SealMonitoringBackendAuthConfig :exec
-- Compare-and-set on the empty ciphertext: the server and the dedicated worker
-- both run the sealing task, and the loser of a race must not overwrite a
-- freshly-sealed envelope with a re-encryption of the document it read before
-- the winner stripped it.
UPDATE monitoring_backends
   SET auth_config_encrypted = $2,
       auth_config = $3
 WHERE id = $1
   AND auth_config_encrypted = '';

-- name: GetClusterMonitoringConfig :one
SELECT * FROM cluster_monitoring_configs WHERE cluster_id = $1;

-- name: UpsertClusterMonitoringConfig :one
INSERT INTO cluster_monitoring_configs (
    cluster_id,
    backend_id,
    cluster_label,
    cluster_label_value,
    scrape_interval_seconds,
    retention,
    stack_namespace,
    prometheus_release_name,
    thanos_sidecar_enabled,
    storage_config_id,
    object_storage_secret_name,
    storage_class,
    storage_size,
    last_applied_spec_hash,
    last_observed_status,
    last_observed_revision,
    last_observed_at,
    last_drift_detected_at,
    status,
    last_healthy_at,
    created_by_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
ON CONFLICT (cluster_id) DO UPDATE SET
    backend_id = EXCLUDED.backend_id,
    cluster_label = EXCLUDED.cluster_label,
    cluster_label_value = EXCLUDED.cluster_label_value,
    scrape_interval_seconds = EXCLUDED.scrape_interval_seconds,
    retention = EXCLUDED.retention,
    stack_namespace = EXCLUDED.stack_namespace,
    prometheus_release_name = EXCLUDED.prometheus_release_name,
    thanos_sidecar_enabled = EXCLUDED.thanos_sidecar_enabled,
    storage_config_id = EXCLUDED.storage_config_id,
    object_storage_secret_name = EXCLUDED.object_storage_secret_name,
    storage_class = EXCLUDED.storage_class,
    storage_size = EXCLUDED.storage_size,
    last_applied_spec_hash = EXCLUDED.last_applied_spec_hash,
    last_observed_status = EXCLUDED.last_observed_status,
    last_observed_revision = EXCLUDED.last_observed_revision,
    last_observed_at = EXCLUDED.last_observed_at,
    last_drift_detected_at = EXCLUDED.last_drift_detected_at,
    status = EXCLUDED.status,
    last_healthy_at = EXCLUDED.last_healthy_at,
    updated_at = now()
RETURNING *;

-- name: GetClusterMonitoringContext :one
SELECT
    cmc.id AS cluster_config_id,
    cmc.cluster_id,
    cmc.backend_id,
    cmc.cluster_label,
    cmc.cluster_label_value,
    cmc.scrape_interval_seconds,
    cmc.retention,
    cmc.stack_namespace,
    cmc.prometheus_release_name,
    cmc.thanos_sidecar_enabled,
    cmc.storage_config_id,
    cmc.object_storage_secret_name,
    cmc.storage_class,
    cmc.storage_size,
    cmc.last_applied_spec_hash,
    cmc.last_observed_status,
    cmc.last_observed_revision,
    cmc.last_observed_at,
    cmc.last_drift_detected_at,
    cmc.status,
    cmc.last_healthy_at,
    mb.name AS backend_name,
    mb.backend_type,
    mb.query_url,
    mb.alertmanager_url,
    mb.tenant_id,
    mb.auth_type,
    mb.auth_config,
    -- Migration 146: the credential half of the pair. Selected here because
    -- alert evaluation and the cluster-metrics handler build a monitoring
    -- client straight off this joined row; without it they would authenticate
    -- with the stripped projection.
    mb.auth_config_encrypted,
    mb.default_step_seconds,
    mb.timeout_seconds
FROM cluster_monitoring_configs cmc
INNER JOIN monitoring_backends mb ON mb.id = cmc.backend_id
WHERE cmc.cluster_id = $1;
