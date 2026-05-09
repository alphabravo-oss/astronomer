-- name: GetProjectByID :one
SELECT * FROM projects WHERE id = $1;

-- name: GetProjectByNameAndCluster :one
SELECT * FROM projects WHERE name = $1 AND cluster_id = $2;

-- name: ListProjects :many
SELECT * FROM projects ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListProjectsByCluster :many
SELECT * FROM projects WHERE cluster_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: CreateProject :one
INSERT INTO projects (name, display_name, description, cluster_id, namespaces, resource_quota, limit_range, network_policy_mode, created_by_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: UpdateProject :one
UPDATE projects SET
    display_name        = $2,
    description         = $3,
    namespaces          = $4,
    resource_quota      = $5,
    limit_range         = $6,
    network_policy_mode = $7,
    updated_at          = now()
WHERE id = $1
RETURNING *;

-- name: DeleteProject :exec
DELETE FROM projects WHERE id = $1;

-- name: CountProjects :one
SELECT count(*) FROM projects;

-- name: CountProjectsByCluster :one
SELECT count(*) FROM projects WHERE cluster_id = $1;

-- name: UpsertProjectNamespace :one
INSERT INTO project_namespaces (project_id, cluster_id, namespace)
VALUES ($1, $2, $3)
ON CONFLICT (project_id, cluster_id, namespace) DO UPDATE
    SET updated_at = now()
RETURNING *;

-- name: DeleteProjectNamespace :exec
DELETE FROM project_namespaces
WHERE project_id = $1 AND cluster_id = $2 AND namespace = $3;

-- name: ListProjectNamespaces :many
SELECT * FROM project_namespaces
WHERE project_id = $1
ORDER BY namespace ASC;

-- name: ListAllProjectNamespaces :many
SELECT * FROM project_namespaces
ORDER BY project_id, cluster_id, namespace;

-- name: ClaimProjectNamespaceReconcile :one
-- Atomically bump the lease so other workers SKIP this row for the given TTL.
-- Returns the row only if we acquired the lease (locked_until expired or null).
UPDATE project_namespaces
SET    locked_until = $4
WHERE  project_id = $1
  AND  cluster_id = $2
  AND  namespace  = $3
  AND  (locked_until IS NULL OR locked_until < now())
RETURNING *;

-- name: MarkProjectNamespaceReconciled :exec
UPDATE project_namespaces
SET    last_reconciled_at   = now(),
       last_reconcile_error = $4,
       locked_until         = NULL,
       updated_at           = now()
WHERE  project_id = $1
  AND  cluster_id = $2
  AND  namespace  = $3;
