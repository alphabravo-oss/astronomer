-- ArgoCD Instances

-- name: GetArgoCDInstanceByID :one
SELECT * FROM argocd_instances WHERE id = $1;

-- name: GetArgoCDInstanceByName :one
SELECT * FROM argocd_instances WHERE name = $1;

-- name: ListArgoCDInstances :many
SELECT * FROM argocd_instances ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListInstancesByCluster :many
SELECT * FROM argocd_instances WHERE cluster_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: CreateArgoCDInstance :one
INSERT INTO argocd_instances (name, cluster_id, api_url, auth_token_encrypted, verify_ssl)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateArgoCDInstance :one
UPDATE argocd_instances SET
    name = $2,
    api_url = $3,
    auth_token_encrypted = $4,
    verify_ssl = $5
WHERE id = $1
RETURNING *;

-- name: UpdateArgoCDInstanceHealth :exec
UPDATE argocd_instances SET is_healthy = $2, last_sync = now() WHERE id = $1;

-- name: DeleteArgoCDInstance :exec
DELETE FROM argocd_instances WHERE id = $1;

-- name: CountArgoCDInstances :one
SELECT count(*) FROM argocd_instances;

-- ArgoCD Applications

-- name: GetArgoCDApplicationByID :one
SELECT * FROM argocd_applications WHERE id = $1;

-- name: GetArgoCDApplicationByName :one
SELECT * FROM argocd_applications WHERE argocd_instance_id = $1 AND name = $2;

-- name: ListArgoCDApplications :many
SELECT * FROM argocd_applications ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListAppsByInstance :many
SELECT * FROM argocd_applications WHERE argocd_instance_id = $1 ORDER BY name ASC LIMIT $2 OFFSET $3;

-- name: ListArgoCDApplicationsByManagedClusterTargets :many
SELECT * FROM argocd_applications
WHERE argocd_instance_id = ANY(sqlc.arg(argocd_instance_ids)::uuid[])
  AND destination_cluster = ANY(sqlc.arg(destination_clusters)::text[])
ORDER BY name ASC;

-- name: UpdateArgoCDApplication :one
UPDATE argocd_applications SET
    project = $2,
    repo_url = $3,
    path = $4,
    target_revision = $5,
    destination_cluster = $6,
    destination_namespace = $7,
    sync_status = $8,
    health_status = $9,
    resource_created_count = $10,
    resource_changed_count = $11,
    resource_pruned_count = $12,
    last_synced = $13
WHERE id = $1
RETURNING *;

-- name: CountArgoCDApplications :one
SELECT count(*) FROM argocd_applications;

-- name: CountArgoCDApplicationsBySyncHealth :many
SELECT
    COALESCE(NULLIF(sync_status, ''), 'Unknown')::text AS sync_status,
    COALESCE(NULLIF(health_status, ''), 'Unknown')::text AS health_status,
    count(*)::bigint AS app_count
FROM argocd_applications
GROUP BY 1, 2
ORDER BY 1, 2;

-- name: CountAppsByInstance :one
SELECT count(*) FROM argocd_applications WHERE argocd_instance_id = $1;

-- ArgoCD Managed Clusters (Phase B1)
-- Index of which of OUR clusters have been registered into each upstream
-- ArgoCD instance. The upstream truth lives in the ArgoCD cluster Secret in
-- the argocd namespace; this table makes list/unregister cheap and gives the
-- ApplicationSet UI something to label-target.

-- name: CreateArgoCDManagedCluster :one
INSERT INTO argocd_managed_clusters (argocd_instance_id, cluster_id, cluster_secret_name, server_url, labels)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (argocd_instance_id, cluster_id) DO UPDATE SET
    cluster_secret_name = EXCLUDED.cluster_secret_name,
    server_url = EXCLUDED.server_url,
    labels = EXCLUDED.labels,
    updated_at = now()
RETURNING *;

-- name: GetArgoCDManagedCluster :one
SELECT * FROM argocd_managed_clusters WHERE argocd_instance_id = $1 AND cluster_id = $2;

-- name: ListArgoCDManagedClusters :many
-- Only registrations whose backing cluster still exists and is NOT
-- decommissioned. Decommission deletes these rows, but this INNER JOIN keeps a
-- tombstoned/deleted-cluster registration from ever surfacing (in the clusters
-- tab, the orphan-app report's valid-target set, or the registration refresh)
-- if a row is ever left behind.
SELECT amc.* FROM argocd_managed_clusters amc
JOIN clusters cl ON cl.id = amc.cluster_id
WHERE amc.argocd_instance_id = $1 AND cl.decommissioned_at IS NULL
ORDER BY amc.created_at ASC;

-- name: ListArgoCDManagedClustersByCluster :many
-- Reverse index of the above: every ArgoCD instance into which a given
-- Astronomer cluster is registered. Used by the
-- "argocd:refresh_managed_cluster_labels" worker task to re-stamp the
-- astronomer.io/label-* keys on every relevant cluster Secret after a
-- clusters.labels mutation.
SELECT * FROM argocd_managed_clusters WHERE cluster_id = $1 ORDER BY created_at ASC;

-- name: DeleteArgoCDManagedCluster :exec
DELETE FROM argocd_managed_clusters WHERE argocd_instance_id = $1 AND cluster_id = $2;

-- name: DeleteArgoCDManagedClustersByCluster :execrows
-- Bulk-delete every (instance, cluster) mapping for one cluster. Used by
-- the decommission worker to drop local rows after a cluster is tombstoned.
-- Upstream Argo cluster Secrets (the actual k8s resource in each Argo
-- namespace) need a separate unregister flow; the orphans are surfaced via
-- the cluster.decommission.argocd_secret_orphan audit row.
DELETE FROM argocd_managed_clusters WHERE cluster_id = $1;

-- name: UpdateArgoCDManagedClusterLabels :one
UPDATE argocd_managed_clusters
SET labels = $3, updated_at = now()
WHERE argocd_instance_id = $1 AND cluster_id = $2
RETURNING *;

-- ArgoCD cluster-proxy service tokens. These are not user API tokens:
-- they are cluster-scoped machine identities used only by built-in ArgoCD
-- to reach an adopted cluster through Astronomer's tunnel-backed proxy.

-- name: GetActiveArgoCDClusterProxyTokenByClusterID :one
SELECT * FROM argocd_cluster_proxy_tokens
WHERE cluster_id = $1
  AND purpose = 'argocd_cluster_proxy'
  AND is_revoked = false
  AND (expires_at IS NULL OR expires_at > now());

-- name: GetArgoCDClusterProxyTokenByHash :one
SELECT * FROM argocd_cluster_proxy_tokens
WHERE token_hash = $1
  AND purpose = 'argocd_cluster_proxy'
  AND is_revoked = false
  AND (expires_at IS NULL OR expires_at > now());

-- name: UpsertArgoCDClusterProxyToken :one
INSERT INTO argocd_cluster_proxy_tokens
    (cluster_id, token_hash, token_prefix, token_encrypted, expires_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (cluster_id, purpose) DO UPDATE SET
    token_hash = EXCLUDED.token_hash,
    token_prefix = EXCLUDED.token_prefix,
    token_encrypted = EXCLUDED.token_encrypted,
    expires_at = EXCLUDED.expires_at,
    is_revoked = false,
    updated_at = now()
RETURNING *;

-- name: TouchArgoCDClusterProxyToken :exec
UPDATE argocd_cluster_proxy_tokens SET last_used_at = now() WHERE id = $1;

-- name: DeleteArgoCDClusterProxyTokensByCluster :execrows
DELETE FROM argocd_cluster_proxy_tokens WHERE cluster_id = $1;
