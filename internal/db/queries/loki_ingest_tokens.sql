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

-- Superusers and users with a global role that grants monitoring:read or
-- monitoring:update (or *). Filter verbs/resources in Go so tests can
-- exercise the same rule helper the reconciler uses.
-- name: ListLokiQueryACLAdminCandidates :many
SELECT u.email, u.is_superuser, COALESCE(gr.rules, '[]'::jsonb) AS rules
FROM users u
LEFT JOIN global_role_bindings grb ON grb.user_id = u.id
LEFT JOIN global_roles gr ON gr.id = grb.role_id
WHERE u.is_active = true AND u.is_service = false
ORDER BY u.email;

-- Cluster bindings plus the bound role rules. Go keeps only logging:read
-- or monitoring:read (or *) so a workload-editor row cannot widen org.
-- name: ListLokiQueryACLUserCandidates :many
SELECT u.email, crb.cluster_id, cr.rules
FROM cluster_role_bindings crb
INNER JOIN users u ON u.id = crb.user_id
INNER JOIN cluster_roles cr ON cr.id = crb.role_id
WHERE u.is_active = true AND u.is_service = false
ORDER BY u.email, crb.cluster_id;
