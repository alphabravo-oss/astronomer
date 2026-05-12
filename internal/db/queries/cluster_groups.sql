-- Migration 066 — cluster groups CRUD + tree expansion.
--
-- The Go shim in internal/db/sqlc/cluster_groups.sql.go hand-implements
-- these (the sqlc CLI is currently disabled in worktrees); the queries
-- below are the canonical source-of-truth for the contract.

-- name: ListClusterGroups :many
SELECT id, name, slug, description, parent_id, color, icon, enabled, created_by, created_at, updated_at
FROM cluster_groups
WHERE enabled = true
ORDER BY name ASC;

-- name: GetClusterGroupByID :one
SELECT id, name, slug, description, parent_id, color, icon, enabled, created_by, created_at, updated_at
FROM cluster_groups
WHERE id = $1;

-- name: CreateClusterGroup :one
INSERT INTO cluster_groups (name, slug, description, parent_id, color, icon, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, name, slug, description, parent_id, color, icon, enabled, created_by, created_at, updated_at;

-- name: UpdateClusterGroup :one
UPDATE cluster_groups
SET name        = $2,
    slug        = $3,
    description = $4,
    parent_id   = $5,
    color       = $6,
    icon        = $7,
    updated_at  = now()
WHERE id = $1
RETURNING id, name, slug, description, parent_id, color, icon, enabled, created_by, created_at, updated_at;

-- name: DeleteClusterGroup :exec
DELETE FROM cluster_groups WHERE id = $1;

-- name: ListClusterGroupsAsTree :many
WITH RECURSIVE tree AS (
    SELECT id, name, slug, description, parent_id, color, icon, enabled, created_by, created_at, updated_at, 0 AS depth
    FROM cluster_groups
    WHERE parent_id IS NULL AND enabled = true
    UNION ALL
    SELECT c.id, c.name, c.slug, c.description, c.parent_id, c.color, c.icon, c.enabled, c.created_by, c.created_at, c.updated_at, t.depth + 1
    FROM cluster_groups c
    INNER JOIN tree t ON c.parent_id = t.id
    WHERE c.enabled = true
)
SELECT id, name, slug, description, parent_id, color, icon, enabled, created_by, created_at, updated_at, depth FROM tree
ORDER BY depth, name;

-- name: ListClustersInGroupTree :many
WITH RECURSIVE subtree AS (
    SELECT id FROM cluster_groups WHERE id = $1 AND enabled = true
    UNION ALL
    SELECT c.id FROM cluster_groups c
    INNER JOIN subtree s ON c.parent_id = s.id
    WHERE c.enabled = true
)
SELECT cl.id, cl.name FROM clusters cl
INNER JOIN subtree s ON cl.group_id = s.id
ORDER BY cl.name ASC;

-- name: CountClustersInGroup :one
SELECT COUNT(*)::bigint FROM clusters WHERE group_id = $1;

-- name: CountClustersInGroupTree :one
WITH RECURSIVE subtree AS (
    SELECT id FROM cluster_groups WHERE id = $1 AND enabled = true
    UNION ALL
    SELECT c.id FROM cluster_groups c
    INNER JOIN subtree s ON c.parent_id = s.id
    WHERE c.enabled = true
)
SELECT COUNT(*)::bigint FROM clusters cl WHERE cl.group_id IN (SELECT id FROM subtree);

-- name: AssignClusterGroup :exec
UPDATE clusters SET group_id = $2, updated_at = now() WHERE id = $1;

-- name: UnassignClusterGroup :exec
UPDATE clusters SET group_id = NULL, updated_at = now() WHERE id = $1;

-- name: GetClusterGroupForCluster :one
SELECT group_id FROM clusters WHERE id = $1;

-- name: CountEnabledClusterGroups :one
SELECT COUNT(*)::bigint FROM cluster_groups WHERE enabled = true;
