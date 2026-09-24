-- name: ListFilteredHelmCharts :many
SELECT c.* FROM helm_charts c
JOIN helm_repositories r ON r.id = c.repository_id
WHERE ((sqlc.arg(global_scope)::boolean AND r.owner_project_id IS NULL)
       OR (NOT sqlc.arg(global_scope)::boolean AND c.repository_id = ANY(sqlc.arg(repository_ids)::uuid[])))
  AND (sqlc.arg(tag)::text = '' OR EXISTS (SELECT 1 FROM helm_chart_tags t WHERE t.chart_id = c.id AND t.tag = sqlc.arg(tag)::text))
  AND (sqlc.arg(search_pattern)::text = ''
       OR c.name ILIKE sqlc.arg(search_pattern)::text ESCAPE E'\\'
       OR COALESCE(c.display_name, '') ILIKE sqlc.arg(search_pattern)::text ESCAPE E'\\'
       OR COALESCE(c.description, '') ILIKE sqlc.arg(search_pattern)::text ESCAPE E'\\')
ORDER BY c.name ASC, c.repository_id ASC, c.id ASC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountFilteredHelmCharts :one
SELECT count(*) FROM helm_charts c
JOIN helm_repositories r ON r.id = c.repository_id
WHERE ((sqlc.arg(global_scope)::boolean AND r.owner_project_id IS NULL)
       OR (NOT sqlc.arg(global_scope)::boolean AND c.repository_id = ANY(sqlc.arg(repository_ids)::uuid[])))
  AND (sqlc.arg(tag)::text = '' OR EXISTS (SELECT 1 FROM helm_chart_tags t WHERE t.chart_id = c.id AND t.tag = sqlc.arg(tag)::text))
  AND (sqlc.arg(search_pattern)::text = ''
       OR c.name ILIKE sqlc.arg(search_pattern)::text ESCAPE E'\\'
       OR COALESCE(c.display_name, '') ILIKE sqlc.arg(search_pattern)::text ESCAPE E'\\'
       OR COALESCE(c.description, '') ILIKE sqlc.arg(search_pattern)::text ESCAPE E'\\');

-- name: ListCatalogProjectsByCluster :many
-- Catalog visibility includes secondary cluster membership without widening
-- the general project inventory API's separate authorization contract.
SELECT projects.* FROM projects
WHERE projects.cluster_id = sqlc.arg(cluster_id)
   OR EXISTS (
     SELECT 1 FROM project_namespaces pn
     WHERE pn.project_id = projects.id AND pn.cluster_id = sqlc.arg(cluster_id)
   )
ORDER BY projects.created_at DESC, projects.id DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);
