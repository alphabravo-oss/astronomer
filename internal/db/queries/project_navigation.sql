-- name: ListProjectNamespaceScopes :many
-- Only the already authorized page of projects; no estate-wide namespace scan.
SELECT * FROM project_namespaces
WHERE project_id = ANY(sqlc.arg(project_ids)::uuid[])
ORDER BY project_id, cluster_id, namespace;

-- name: ListClusterProjectsForScopes :many
SELECT projects.* FROM projects
WHERE (projects.cluster_id = sqlc.arg(selected_cluster_id) OR EXISTS (
    SELECT 1 FROM project_namespaces pn
    WHERE pn.project_id = projects.id AND pn.cluster_id = sqlc.arg(selected_cluster_id)
))
AND (sqlc.arg(all_scopes)::boolean OR projects.id = ANY(sqlc.arg(project_ids)::uuid[])
    OR projects.cluster_id = ANY(sqlc.arg(cluster_ids)::uuid[]))
AND (sqlc.arg(filter_search)::text = '' OR name ILIKE '%' || sqlc.arg(filter_search) || '%'
    OR display_name ILIKE '%' || sqlc.arg(filter_search) || '%'
    OR description ILIKE '%' || sqlc.arg(filter_search) || '%')
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountClusterProjectsForScopes :one
SELECT count(*) FROM projects
WHERE (projects.cluster_id = sqlc.arg(selected_cluster_id) OR EXISTS (
    SELECT 1 FROM project_namespaces pn
    WHERE pn.project_id = projects.id AND pn.cluster_id = sqlc.arg(selected_cluster_id)
))
AND (sqlc.arg(all_scopes)::boolean OR projects.id = ANY(sqlc.arg(project_ids)::uuid[])
    OR projects.cluster_id = ANY(sqlc.arg(cluster_ids)::uuid[]))
AND (sqlc.arg(filter_search)::text = '' OR name ILIKE '%' || sqlc.arg(filter_search) || '%'
    OR display_name ILIKE '%' || sqlc.arg(filter_search) || '%'
    OR description ILIKE '%' || sqlc.arg(filter_search) || '%');
