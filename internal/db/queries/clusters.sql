-- name: GetClusterByID :one
SELECT * FROM clusters WHERE id = $1;

-- name: EnsureLocalCluster :one
-- Idempotently create-or-return the singleton "local" cluster row that
-- represents the Kubernetes cluster the server itself runs in. Uses a CTE
-- so the round-trip both inserts (when no local row exists yet) and selects
-- (when one already does). The clusters_one_local partial unique index makes
-- the ON CONFLICT branch reachable; if the conflicting row was inserted by a
-- concurrent server replica, the SELECT in the UNION returns it.
WITH inserted AS (
    INSERT INTO clusters (
        name,
        display_name,
        description,
        status,
        api_server_url,
        distribution,
        kubernetes_version,
        node_count,
        is_local,
        environment,
        provider
    )
    SELECT
        sqlc.arg(name)::varchar,
        sqlc.arg(display_name)::varchar,
        sqlc.arg(description)::text,
        sqlc.arg(status)::varchar,
        sqlc.arg(api_server_url)::varchar,
        sqlc.arg(distribution)::varchar,
        sqlc.arg(kubernetes_version)::varchar,
        sqlc.arg(node_count)::integer,
        true,
        'production',
        'other'
    WHERE NOT EXISTS (SELECT 1 FROM clusters WHERE is_local = true)
    ON CONFLICT DO NOTHING
    RETURNING *
)
SELECT * FROM inserted
UNION ALL
SELECT * FROM clusters WHERE is_local = true AND NOT EXISTS (SELECT 1 FROM inserted)
LIMIT 1;

-- name: GetClusterByName :one
SELECT * FROM clusters WHERE name = $1 AND decommissioned_at IS NULL;

-- name: ListClusters :many
-- Excludes tombstoned (sprint 038) rows. Decommissioned clusters keep
-- their row in the DB for forensics but never appear in the UI list.
SELECT * FROM clusters WHERE decommissioned_at IS NULL ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListClustersByStatus :many
SELECT * FROM clusters WHERE status = sqlc.arg(status) AND decommissioned_at IS NULL ORDER BY created_at DESC LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CreateCluster :one
INSERT INTO clusters (name, display_name, description, environment, region, provider, distribution, created_by_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateCluster :one
UPDATE clusters SET
    display_name = $2,
    description = $3,
    environment = $4,
    region = $5,
    labels = $6,
    annotations = $7
WHERE id = $1
RETURNING *;

-- name: UpdateClusterStatus :exec
UPDATE clusters SET status = $2 WHERE id = $1;

-- name: UpdateClusterHeartbeat :exec
UPDATE clusters SET
    last_heartbeat = now(),
    agent_version = $2,
    kubernetes_version = $3,
    node_count = $4,
    distribution = $5
WHERE id = $1;

-- name: DeleteCluster :exec
DELETE FROM clusters WHERE id = $1;

-- name: CountClusters :one
SELECT count(*) FROM clusters WHERE decommissioned_at IS NULL;

-- name: GetClusterHealthStatus :one
SELECT * FROM cluster_health_statuses WHERE cluster_id = $1;

-- name: UpsertClusterHealthStatus :one
INSERT INTO cluster_health_statuses (cluster_id, cpu_usage_percent, memory_usage_percent, pod_count, node_count, conditions)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (cluster_id) DO UPDATE SET
    cpu_usage_percent = EXCLUDED.cpu_usage_percent,
    memory_usage_percent = EXCLUDED.memory_usage_percent,
    pod_count = EXCLUDED.pod_count,
    node_count = EXCLUDED.node_count,
    conditions = EXCLUDED.conditions,
    last_check = now()
RETURNING *;

-- name: CreateClusterRegistrationToken :one
INSERT INTO cluster_registration_tokens (cluster_id, token, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetRegistrationTokenByToken :one
-- The is_used filter is intentionally NOT applied: until the server issues a
-- long-lived agent token in CONNECT_ACK, the same registration token is the
-- only credential the agent has, and reconnect attempts must succeed up to
-- expires_at. is_used remains a tracking column for the future flow.
SELECT * FROM cluster_registration_tokens WHERE token = $1 AND expires_at > now();

-- name: MarkRegistrationTokenUsed :exec
UPDATE cluster_registration_tokens SET is_used = true WHERE id = $1;

-- name: GetClusterAgentTokenByClusterID :one
SELECT * FROM cluster_agent_tokens WHERE cluster_id = $1;

-- name: GetClusterAgentTokenByToken :one
SELECT * FROM cluster_agent_tokens WHERE token = $1;

-- name: UpsertClusterAgentToken :one
INSERT INTO cluster_agent_tokens (cluster_id, token, last_used_at)
VALUES ($1, $2, now())
ON CONFLICT (cluster_id) DO UPDATE SET
    token = EXCLUDED.token,
    last_used_at = now()
RETURNING *;

-- name: TouchClusterAgentToken :exec
UPDATE cluster_agent_tokens SET last_used_at = now() WHERE id = $1;

-- name: DeleteExpiredRegistrationTokens :execrows
DELETE FROM cluster_registration_tokens WHERE expires_at < now() OR (is_used = true AND updated_at < now() - INTERVAL '7 days');

-- name: GetClusterRegistryConfig :one
SELECT * FROM cluster_registry_configs WHERE cluster_id = $1;

-- name: UpsertClusterRegistryConfig :one
INSERT INTO cluster_registry_configs (cluster_id, private_registry_url, registry_username, registry_password, insecure, ca_bundle)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (cluster_id) DO UPDATE SET
    private_registry_url = EXCLUDED.private_registry_url,
    registry_username = EXCLUDED.registry_username,
    registry_password = EXCLUDED.registry_password,
    insecure = EXCLUDED.insecure,
    ca_bundle = EXCLUDED.ca_bundle
RETURNING *;

-- name: DeleteClusterRegistryConfig :exec
DELETE FROM cluster_registry_configs WHERE cluster_id = $1;

-- Migration 050: multi-registry-per-cluster CRUD. The legacy
-- Get/Upsert/Delete by cluster_id above is kept for back-compat with the old
-- single-registry route; the queries below operate on the row id so multiple
-- registry configs can co-exist under one cluster.

-- name: ListClusterRegistryConfigs :many
SELECT * FROM cluster_registry_configs
WHERE cluster_id = $1
ORDER BY created_at ASC;

-- name: ListAllClusterRegistryConfigs :many
-- Used by the drift-reconcile sweep — walks every row across every cluster.
SELECT * FROM cluster_registry_configs
ORDER BY cluster_id, created_at ASC;

-- name: GetClusterRegistryConfigByID :one
SELECT * FROM cluster_registry_configs WHERE id = $1;

-- name: CreateClusterRegistryConfig :one
INSERT INTO cluster_registry_configs (
    cluster_id,
    private_registry_url,
    registry_username,
    registry_password,
    insecure,
    ca_bundle,
    namespaces,
    inject_default_sa,
    secret_name
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: UpdateClusterRegistryConfig :one
UPDATE cluster_registry_configs SET
    private_registry_url = $2,
    registry_username    = $3,
    registry_password    = $4,
    insecure             = $5,
    ca_bundle            = $6,
    namespaces           = $7,
    inject_default_sa    = $8,
    secret_name          = $9,
    updated_at           = now()
WHERE id = $1
RETURNING *;

-- name: DeleteClusterRegistryConfigByID :exec
DELETE FROM cluster_registry_configs WHERE id = $1;

-- name: MarkClusterRegistryApplied :exec
UPDATE cluster_registry_configs SET
    last_applied_at  = now(),
    last_apply_error = ''
WHERE id = $1;

-- name: MarkClusterRegistryApplyError :exec
UPDATE cluster_registry_configs SET
    last_apply_error = $2
WHERE id = $1;

-- name: ListClusterConditions :many
SELECT * FROM cluster_conditions WHERE cluster_id = $1 ORDER BY type;

-- name: UpsertClusterCondition :one
-- Match metav1.Condition semantics: when status flips, bump
-- last_transition_time; on every probe, bump last_probe_time and
-- updated_at. reason/message are always refreshed.
INSERT INTO cluster_conditions (
    cluster_id, type, status, reason, message,
    last_transition_time, last_probe_time
) VALUES (
    $1, $2, $3, $4, $5, now(), now()
)
ON CONFLICT (cluster_id, type) DO UPDATE SET
    status               = EXCLUDED.status,
    reason               = EXCLUDED.reason,
    message              = EXCLUDED.message,
    last_probe_time      = now(),
    last_transition_time = CASE
        WHEN cluster_conditions.status = EXCLUDED.status
            THEN cluster_conditions.last_transition_time
            ELSE now()
        END,
    updated_at           = now()
RETURNING *;

-- name: DeleteClusterConditionsForCluster :exec
DELETE FROM cluster_conditions WHERE cluster_id = $1;
