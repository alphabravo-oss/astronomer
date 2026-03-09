-- name: GetProjectByID :one
SELECT * FROM projects WHERE id = $1;

-- name: GetProjectByNameAndCluster :one
SELECT * FROM projects WHERE name = $1 AND cluster_id = $2;

-- name: ListProjects :many
SELECT * FROM projects ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListProjectsByCluster :many
SELECT * FROM projects WHERE cluster_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: CreateProject :one
INSERT INTO projects (name, display_name, description, cluster_id, namespaces, resource_quota, created_by_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateProject :one
UPDATE projects SET
    display_name = $2,
    description = $3,
    namespaces = $4,
    resource_quota = $5
WHERE id = $1
RETURNING *;

-- name: DeleteProject :exec
DELETE FROM projects WHERE id = $1;

-- name: CountProjects :one
SELECT count(*) FROM projects;

-- name: CountProjectsByCluster :one
SELECT count(*) FROM projects WHERE cluster_id = $1;
