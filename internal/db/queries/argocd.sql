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

-- name: ListAppsByInstanceAndProject :many
SELECT * FROM argocd_applications WHERE argocd_instance_id = $1 AND project = $2 ORDER BY name ASC LIMIT $3 OFFSET $4;

-- name: CreateArgoCDApplication :one
INSERT INTO argocd_applications (argocd_instance_id, name, project, repo_url, path, target_revision, destination_cluster, destination_namespace, sync_status, health_status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

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
    last_synced = $10
WHERE id = $1
RETURNING *;

-- name: DeleteArgoCDApplication :exec
DELETE FROM argocd_applications WHERE id = $1;

-- name: CountArgoCDApplications :one
SELECT count(*) FROM argocd_applications;

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
SELECT * FROM argocd_managed_clusters WHERE argocd_instance_id = $1 ORDER BY created_at ASC;

-- name: ListArgoCDManagedClustersByCluster :many
-- Reverse index of the above: every ArgoCD instance into which a given
-- Astronomer cluster is registered. Used by the
-- "argocd:refresh_managed_cluster_labels" worker task to re-stamp the
-- astronomer.io/label-* keys on every relevant cluster Secret after a
-- clusters.labels mutation.
SELECT * FROM argocd_managed_clusters WHERE cluster_id = $1 ORDER BY created_at ASC;

-- name: DeleteArgoCDManagedCluster :exec
DELETE FROM argocd_managed_clusters WHERE argocd_instance_id = $1 AND cluster_id = $2;

-- name: UpdateArgoCDManagedClusterLabels :one
UPDATE argocd_managed_clusters
SET labels = $3, updated_at = now()
WHERE argocd_instance_id = $1 AND cluster_id = $2
RETURNING *;
