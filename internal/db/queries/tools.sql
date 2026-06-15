-- name: GetClusterToolByID :one
SELECT * FROM cluster_tools WHERE id = $1;

-- name: GetToolBySlug :one
SELECT * FROM cluster_tools WHERE slug = $1;

-- name: ListClusterTools :many
SELECT * FROM cluster_tools ORDER BY category, name ASC LIMIT $1 OFFSET $2;

-- name: ListEnabledTools :many
SELECT * FROM cluster_tools WHERE is_enabled = true ORDER BY category, name ASC;

-- name: CountClusterTools :one
SELECT count(*) FROM cluster_tools;
