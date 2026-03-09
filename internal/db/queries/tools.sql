-- name: GetClusterToolByID :one
SELECT * FROM cluster_tools WHERE id = $1;

-- name: GetToolBySlug :one
SELECT * FROM cluster_tools WHERE slug = $1;

-- name: ListClusterTools :many
SELECT * FROM cluster_tools ORDER BY category, name ASC LIMIT $1 OFFSET $2;

-- name: ListEnabledTools :many
SELECT * FROM cluster_tools WHERE is_enabled = true ORDER BY category, name ASC;

-- name: ListToolsByCategory :many
SELECT * FROM cluster_tools WHERE category = $1 ORDER BY name ASC;

-- name: CreateClusterTool :one
INSERT INTO cluster_tools (slug, name, description, icon, category, charts, version_constraint, default_namespace, is_builtin, is_enabled, helm_chart_id, presets, service_name, service_port, service_path, sub_services)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
RETURNING *;

-- name: UpdateClusterTool :one
UPDATE cluster_tools SET
    name = $2,
    description = $3,
    icon = $4,
    category = $5,
    charts = $6,
    version_constraint = $7,
    default_namespace = $8,
    is_enabled = $9,
    helm_chart_id = $10,
    presets = $11,
    service_name = $12,
    service_port = $13,
    service_path = $14,
    sub_services = $15
WHERE id = $1
RETURNING *;

-- name: UpdateToolEnabled :exec
UPDATE cluster_tools SET is_enabled = $2 WHERE id = $1;

-- name: DeleteClusterTool :exec
DELETE FROM cluster_tools WHERE id = $1;

-- name: CountClusterTools :one
SELECT count(*) FROM cluster_tools;
