-- name: GetClusterByID :one
SELECT * FROM clusters WHERE id = $1;

-- name: GetClusterByName :one
SELECT * FROM clusters WHERE name = $1;

-- name: ListClusters :many
SELECT * FROM clusters ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListClustersByStatus :many
SELECT * FROM clusters WHERE status = sqlc.arg(status) ORDER BY created_at DESC LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CreateCluster :one
INSERT INTO clusters (name, display_name, description, environment, region, provider, created_by_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
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
SELECT count(*) FROM clusters;

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
SELECT * FROM cluster_registration_tokens WHERE token = $1 AND is_used = false AND expires_at > now();

-- name: MarkRegistrationTokenUsed :exec
UPDATE cluster_registration_tokens SET is_used = true WHERE id = $1;

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
