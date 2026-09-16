-- name: RecordAgentHeartbeat :one
-- Persist the ordinary heartbeat path in one round trip:
--   1. touch the authenticated connection row;
--   2. advance the narrow liveness row;
--   3. update wide cluster inventory only when it materially changed; and
--   4. refresh health in the same statement.
--
-- A missing live connection returns no row. That prevents a superseded socket
-- from advancing liveness after another replica has replaced its session.
WITH touched_connection AS (
    UPDATE agent_connections
    SET last_ping = statement_timestamp()
    WHERE agent_connections.id = sqlc.arg(connection_id)
      AND agent_connections.cluster_id = sqlc.arg(cluster_id)
      AND agent_connections.status = 'connected'
    RETURNING agent_connections.id
), liveness AS (
    INSERT INTO cluster_liveness (
        cluster_id,
        last_heartbeat,
        heartbeat_count,
        commands_pending
    )
    SELECT
        sqlc.arg(cluster_id),
        statement_timestamp(),
        1,
        EXISTS (
            SELECT 1
            FROM agent_lifecycle_operations op
            WHERE op.cluster_id = sqlc.arg(cluster_id)
              AND op.status IN ('pending', 'running')
        )
    FROM touched_connection
    ON CONFLICT (cluster_id) DO UPDATE SET
        last_heartbeat = EXCLUDED.last_heartbeat,
        heartbeat_count = cluster_liveness.heartbeat_count + 1,
        updated_at = EXCLUDED.last_heartbeat
    RETURNING commands_pending
), inventory AS (
    UPDATE clusters
    SET agent_version = COALESCE(NULLIF(sqlc.arg(agent_version)::text, ''), agent_version),
        kubernetes_version = COALESCE(NULLIF(sqlc.arg(kubernetes_version)::text, ''), kubernetes_version),
        node_count = CASE WHEN sqlc.arg(node_count)::int > 0 THEN sqlc.arg(node_count)::int ELSE node_count END,
        distribution = COALESCE(NULLIF(sqlc.arg(distribution)::text, ''), distribution)
    WHERE clusters.id = sqlc.arg(cluster_id)
      AND clusters.decommissioned_at IS NULL
      AND EXISTS (SELECT 1 FROM liveness)
      AND (
          clusters.agent_version IS DISTINCT FROM COALESCE(NULLIF(sqlc.arg(agent_version)::text, ''), clusters.agent_version)
          OR clusters.kubernetes_version IS DISTINCT FROM COALESCE(NULLIF(sqlc.arg(kubernetes_version)::text, ''), clusters.kubernetes_version)
          OR clusters.node_count IS DISTINCT FROM CASE WHEN sqlc.arg(node_count)::int > 0 THEN sqlc.arg(node_count)::int ELSE clusters.node_count END
          OR clusters.distribution IS DISTINCT FROM COALESCE(NULLIF(sqlc.arg(distribution)::text, ''), clusters.distribution)
      )
    RETURNING id
), health AS (
    INSERT INTO cluster_health_statuses (
        cluster_id,
        cpu_usage_percent,
        memory_usage_percent,
        pod_count,
        node_count,
        conditions
    )
    SELECT
        sqlc.arg(cluster_id),
        sqlc.arg(cpu_usage_percent),
        sqlc.arg(memory_usage_percent),
        sqlc.arg(pod_count),
        sqlc.arg(node_count),
        sqlc.arg(conditions)
    FROM liveness
    ON CONFLICT (cluster_id) DO UPDATE SET
        cpu_usage_percent = EXCLUDED.cpu_usage_percent,
        memory_usage_percent = EXCLUDED.memory_usage_percent,
        pod_count = EXCLUDED.pod_count,
        node_count = EXCLUDED.node_count,
        conditions = EXCLUDED.conditions,
        last_check = statement_timestamp()
    RETURNING cluster_id
)
SELECT commands_pending FROM liveness;

-- name: GetClusterLiveness :one
SELECT * FROM cluster_liveness WHERE cluster_id = $1;

-- name: ListClusterLivenessForClusters :many
SELECT * FROM cluster_liveness
WHERE cluster_id = ANY(sqlc.arg(cluster_ids)::uuid[]);

-- name: GetClusterHealthTarget :one
SELECT
    c.id,
    c.status,
    c.kubernetes_version,
    c.distribution,
    c.node_count,
    l.last_heartbeat
FROM clusters c
LEFT JOIN cluster_liveness l ON l.cluster_id = c.id
WHERE c.id = sqlc.arg(cluster_id)
  AND c.decommissioned_at IS NULL;

-- name: ListClusterHealthTargets :many
SELECT
    c.id,
    c.status,
    c.kubernetes_version,
    c.distribution,
    c.node_count,
    l.last_heartbeat
FROM clusters c
LEFT JOIN cluster_liveness l ON l.cluster_id = c.id
WHERE c.decommissioned_at IS NULL
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);
