-- name: ListConnectionsByCluster :many
SELECT * FROM agent_connections WHERE cluster_id = $1 ORDER BY connected_at DESC LIMIT $2 OFFSET $3;

-- name: ListActiveConnections :many
SELECT * FROM agent_connections WHERE status = 'connected' ORDER BY connected_at DESC;

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
