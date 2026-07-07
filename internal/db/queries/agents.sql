-- name: ListConnectionsByCluster :many
SELECT * FROM agent_connections WHERE cluster_id = $1 ORDER BY connected_at DESC LIMIT $2 OFFSET $3;

-- name: ListActiveConnections :many
SELECT * FROM agent_connections WHERE status = 'connected' ORDER BY connected_at DESC;

-- name: ListLatestConnectionsByClusters :many
SELECT DISTINCT ON (cluster_id) *
FROM agent_connections
WHERE cluster_id = ANY(sqlc.arg(cluster_ids)::uuid[])
ORDER BY cluster_id, connected_at DESC;

-- name: ListClusterConnectionStatus :many
-- One row per non-decommissioned cluster with the status + last-activity time of
-- its most-recent agent connection ('never' / NULL when it has never connected).
-- Drives the always-present per-cluster agent_connections gauge so the metric
-- series SURVIVES disconnect (O-03): a disconnected cluster still emits a
-- 0-valued sample instead of the series vanishing (which a threshold alert
-- can never fire on). COALESCE keeps the non-timestamp columns non-null so the
-- LEFT JOIN never yields a NULL into a non-nullable scan target.
SELECT
    c.id   AS cluster_id,
    c.name AS cluster_name,
    COALESCE(lc.status, 'never') AS status,
    -- c.created_at is the guaranteed-non-null final fallback so a never-connected
    -- cluster still scans (sqlc types this column non-null); its age is harmless
    -- because such a cluster reports connections=0 anyway.
    COALESCE(lc.last_ping, lc.disconnected_at, lc.connected_at, c.created_at) AS last_activity
FROM clusters c
LEFT JOIN LATERAL (
    SELECT ac.status, ac.connected_at, ac.last_ping, ac.disconnected_at
    FROM agent_connections ac
    WHERE ac.cluster_id = c.id
    ORDER BY ac.connected_at DESC
    LIMIT 1
) lc ON true
WHERE c.decommissioned_at IS NULL;

-- name: CreateAgentConnection :one
INSERT INTO agent_connections (cluster_id, agent_id, session_id, status, channel_name, pod_name, node_name, agent_version)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateAgentConnectionStatus :exec
UPDATE agent_connections SET status = $2, disconnected_at = $3 WHERE id = $1;

-- name: DisconnectActiveConnectionsByCluster :exec
UPDATE agent_connections
SET status = 'disconnected', disconnected_at = now()
WHERE cluster_id = $1 AND status = 'connected';

-- name: UpdateAgentConnectionPing :exec
UPDATE agent_connections SET last_ping = now() WHERE id = $1;
