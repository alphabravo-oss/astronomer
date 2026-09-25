-- name: GetProjectByID :one
SELECT * FROM projects WHERE id = $1;

-- name: GetProjectByIDForUpdate :one
-- Row-locks the project row so AddNamespace / RemoveNamespace serialize their
-- read-modify-write of the namespaces JSONB list, preventing concurrent
-- membership changes from clobbering each other (last-writer-wins). Must run
-- inside a transaction alongside the UpdateProject + project_namespaces write.
SELECT * FROM projects WHERE id = $1 FOR UPDATE;

-- name: GetProjectByNameAndCluster :one
SELECT * FROM projects WHERE name = $1 AND cluster_id = $2;

-- name: GetProjectNamespaceByClusterAndNamespace :one
-- Resolve optional project ownership from the Kubernetes deployment target.
-- The partial unique index on (cluster_id, namespace) guarantees at most one
-- owning project, so callers never need a user-selected project discriminator.
SELECT * FROM project_namespaces
WHERE cluster_id = $1 AND namespace = $2;

-- name: ListProjects :many
SELECT p.* FROM projects p
JOIN clusters c ON c.id = p.cluster_id
WHERE (
    sqlc.arg(filter_search)::text = ''
    OR p.name ILIKE '%' || sqlc.arg(filter_search) || '%'
    OR p.display_name ILIKE '%' || sqlc.arg(filter_search) || '%'
    OR p.description ILIKE '%' || sqlc.arg(filter_search) || '%'
)
AND c.decommissioned_at IS NULL
ORDER BY p.created_at DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountProjectsFiltered :one
SELECT count(*) FROM projects p
JOIN clusters c ON c.id = p.cluster_id
WHERE (
    sqlc.arg(filter_search)::text = ''
    OR p.name ILIKE '%' || sqlc.arg(filter_search) || '%'
    OR p.display_name ILIKE '%' || sqlc.arg(filter_search) || '%'
    OR p.description ILIKE '%' || sqlc.arg(filter_search) || '%'
)
AND c.decommissioned_at IS NULL;

-- name: ListProjectsForScopes :many
-- Scope-filtered ListProjects: the projects the caller is bound to directly,
-- plus every project on a cluster they hold the grant over WITHOUT a namespace
-- narrowing (a Cluster Owner sees their cluster's projects; a caller confined to
-- one namespace of that cluster does not — see rbac.NarrowedClustersExcluded).
-- Same ordering as ListProjects.
SELECT p.* FROM projects p
JOIN clusters c ON c.id = p.cluster_id
WHERE (
    p.id = ANY(sqlc.arg(project_ids)::uuid[])
    OR p.cluster_id = ANY(sqlc.arg(cluster_ids)::uuid[])
)
AND (
    sqlc.arg(filter_search)::text = ''
    OR p.name ILIKE '%' || sqlc.arg(filter_search) || '%'
    OR p.display_name ILIKE '%' || sqlc.arg(filter_search) || '%'
    OR p.description ILIKE '%' || sqlc.arg(filter_search) || '%'
)
AND c.decommissioned_at IS NULL
ORDER BY p.created_at DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountProjectsForScopes :one
-- Total for a ListProjectsForScopes page; predicate MUST match it exactly (see
-- CountClustersForScopes).
SELECT count(*) FROM projects p
JOIN clusters c ON c.id = p.cluster_id
WHERE (
    p.id = ANY(sqlc.arg(project_ids)::uuid[])
    OR p.cluster_id = ANY(sqlc.arg(cluster_ids)::uuid[])
)
AND (
    sqlc.arg(filter_search)::text = ''
    OR p.name ILIKE '%' || sqlc.arg(filter_search) || '%'
    OR p.display_name ILIKE '%' || sqlc.arg(filter_search) || '%'
    OR p.description ILIKE '%' || sqlc.arg(filter_search) || '%'
)
AND c.decommissioned_at IS NULL;

-- name: ListProjectsByCluster :many
SELECT p.* FROM projects p
JOIN clusters c ON c.id = p.cluster_id
WHERE p.cluster_id = sqlc.arg(cluster_id)
  AND c.decommissioned_at IS NULL
  AND (
    sqlc.arg(filter_search)::text = ''
    OR p.name ILIKE '%' || sqlc.arg(filter_search) || '%'
    OR p.display_name ILIKE '%' || sqlc.arg(filter_search) || '%'
    OR p.description ILIKE '%' || sqlc.arg(filter_search) || '%'
  )
ORDER BY p.created_at DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountProjectsByClusterFiltered :one
SELECT count(*) FROM projects p
JOIN clusters c ON c.id = p.cluster_id
WHERE p.cluster_id = sqlc.arg(cluster_id)
  AND c.decommissioned_at IS NULL
  AND (
    sqlc.arg(filter_search)::text = ''
    OR p.name ILIKE '%' || sqlc.arg(filter_search) || '%'
    OR p.display_name ILIKE '%' || sqlc.arg(filter_search) || '%'
    OR p.description ILIKE '%' || sqlc.arg(filter_search) || '%'
  );

-- name: CreateProject :one
INSERT INTO projects (
    name, display_name, description, cluster_id, namespaces, resource_quota,
    limit_range, network_policy_mode, created_by_id,
    pod_security_profile, resource_quota_cpu_limit, resource_quota_memory_limit, resource_quota_pod_count
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: UpdateProject :one
UPDATE projects SET
    display_name                  = $2,
    description                   = $3,
    namespaces                    = $4,
    resource_quota                = $5,
    limit_range                   = $6,
    network_policy_mode           = $7,
    pod_security_profile          = $8,
    resource_quota_cpu_limit      = $9,
    resource_quota_memory_limit   = $10,
    resource_quota_pod_count      = $11,
    updated_at                    = now()
WHERE id = $1
RETURNING *;

-- name: UpdateProjectPolicy :one
-- Updates only the per-project policy fields without touching membership /
-- namespaces / description metadata. Used by the policy PATCH endpoint so an
-- admin can retune PSS / quota without re-asserting the project's namespace
-- list (which would cause an unnecessary reconcile cascade).
UPDATE projects SET
    pod_security_profile          = $2,
    resource_quota_cpu_limit      = $3,
    resource_quota_memory_limit   = $4,
    resource_quota_pod_count      = $5,
    network_policy_mode           = $6,
    updated_at                    = now()
WHERE id = $1
RETURNING *;

-- name: DeleteProject :exec
DELETE FROM projects WHERE id = $1;

-- name: CountProjects :one
SELECT count(*) FROM projects p
JOIN clusters c ON c.id = p.cluster_id
WHERE c.decommissioned_at IS NULL;

-- name: CountProjectsByCluster :one
SELECT count(*) FROM projects p
JOIN clusters c ON c.id = p.cluster_id
WHERE p.cluster_id = $1 AND c.decommissioned_at IS NULL;

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

-- name: UpsertProjectResourceQuotaAllocation :one
INSERT INTO project_resource_quota_allocations (
    project_id, cluster_id, namespace, cpu_limit, memory_limit, pod_count
)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (project_id, cluster_id, namespace) DO UPDATE SET
    cpu_limit = EXCLUDED.cpu_limit,
    memory_limit = EXCLUDED.memory_limit,
    pod_count = EXCLUDED.pod_count,
    applied_at = now()
RETURNING *;

-- name: DeleteProjectResourceQuotaAllocation :exec
DELETE FROM project_resource_quota_allocations
WHERE project_id = $1 AND cluster_id = $2 AND namespace = $3;

-- name: ListProjectResourceQuotaAllocations :many
SELECT * FROM project_resource_quota_allocations
WHERE project_id = $1
ORDER BY cluster_id, namespace;

-- name: ListAllProjectNamespaces :many
SELECT pn.*
FROM project_namespaces pn
JOIN clusters c ON c.id = pn.cluster_id
WHERE c.decommissioned_at IS NULL
  AND c.status = 'active'
ORDER BY pn.project_id, pn.cluster_id, pn.namespace;

-- name: ClaimProjectNamespaceReconcile :one
-- Atomically bump the lease so other workers SKIP this row for the given TTL.
-- Returns the row only if we acquired the lease (locked_until expired or null).
UPDATE project_namespaces
SET    locked_until = sqlc.arg(locked_until),
       reconcile_claim_token = sqlc.arg(claim_token)
WHERE  project_id = $1
  AND  cluster_id = $2
  AND  namespace  = $3
  AND  (locked_until IS NULL OR locked_until < now())
RETURNING *;

-- name: MarkProjectNamespaceReconciled :execrows
UPDATE project_namespaces
SET    last_reconciled_at   = now(),
       last_reconcile_error = $4,
       locked_until         = NULL,
       reconcile_claim_token = NULL,
       updated_at           = now()
WHERE  project_id = $1
  AND  cluster_id = $2
  AND  namespace  = $3
  AND  reconcile_claim_token = sqlc.arg(claim_token);
