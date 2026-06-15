-- name: GetDefaultMonitoringBackend :one
SELECT * FROM monitoring_backends
WHERE is_default = true OR name = 'default'
ORDER BY is_default DESC, created_at ASC
LIMIT 1;

-- name: UpsertDefaultMonitoringBackend :one
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
    default_step_seconds,
    timeout_seconds,
    is_default,
    created_by_id
)
VALUES ('default', $1, $2, $3, $4, $5, $6, $7, $8, true, $9)
ON CONFLICT (name) DO UPDATE SET
    backend_type = EXCLUDED.backend_type,
    query_url = EXCLUDED.query_url,
    alertmanager_url = EXCLUDED.alertmanager_url,
    tenant_id = EXCLUDED.tenant_id,
    auth_type = EXCLUDED.auth_type,
    auth_config = EXCLUDED.auth_config,
    default_step_seconds = EXCLUDED.default_step_seconds,
    timeout_seconds = EXCLUDED.timeout_seconds,
    is_default = true,
    updated_at = now()
RETURNING *;

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
    mb.default_step_seconds,
    mb.timeout_seconds
FROM cluster_monitoring_configs cmc
INNER JOIN monitoring_backends mb ON mb.id = cmc.backend_id
WHERE cmc.cluster_id = $1;
