-- name: GetLokiIngestTokenByCluster :one
SELECT * FROM loki_ingest_tokens WHERE cluster_id = $1;

-- name: ListLokiIngestTokenHashes :many
SELECT cluster_id, token_hash FROM loki_ingest_tokens ORDER BY cluster_id;

-- name: CountLokiIngestTokens :one
SELECT count(*) FROM loki_ingest_tokens;

-- name: UpsertLokiIngestToken :one
INSERT INTO loki_ingest_tokens (cluster_id, token_hash, token_encrypted, created_by_id, rotated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (cluster_id) DO UPDATE SET
    token_hash = EXCLUDED.token_hash,
    token_encrypted = EXCLUDED.token_encrypted,
    rotated_at = now()
RETURNING *;

-- name: DeleteLokiIngestTokenByCluster :exec
DELETE FROM loki_ingest_tokens WHERE cluster_id = $1;

-- Superusers are fleet Loki admins. v1 ACL is coarse: any cluster-role
-- binding grants that user's email the bound cluster UUID.
-- name: ListLokiQueryACLAdmins :many
SELECT email FROM users
WHERE is_active = true AND is_service = false AND is_superuser = true
ORDER BY email;

-- name: ListLokiQueryACLUserClusters :many
SELECT u.email, crb.cluster_id
FROM cluster_role_bindings crb
INNER JOIN users u ON u.id = crb.user_id
WHERE u.is_active = true AND u.is_service = false
ORDER BY u.email, crb.cluster_id;
