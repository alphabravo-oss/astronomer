-- name: ListFilteredHelmChartsAfter :many
SELECT c.* FROM helm_charts c
JOIN helm_repositories r ON r.id = c.repository_id
WHERE ((sqlc.arg(global_scope)::boolean AND r.owner_project_id IS NULL)
       OR (NOT sqlc.arg(global_scope)::boolean AND c.repository_id = ANY(sqlc.arg(repository_ids)::uuid[])))
  AND (sqlc.arg(tag)::text = '' OR EXISTS (SELECT 1 FROM helm_chart_tags t WHERE t.chart_id = c.id AND t.tag = sqlc.arg(tag)::text))
  AND (sqlc.arg(search_pattern)::text = ''
       OR c.name ILIKE sqlc.arg(search_pattern)::text ESCAPE E'\\'
       OR COALESCE(c.display_name, '') ILIKE sqlc.arg(search_pattern)::text ESCAPE E'\\'
       OR COALESCE(c.description, '') ILIKE sqlc.arg(search_pattern)::text ESCAPE E'\\')
AND (c.name, c.repository_id, c.id) > (sqlc.arg(after_name)::text, sqlc.arg(after_repository_id)::uuid, sqlc.arg(after_id)::uuid)
ORDER BY c.name ASC, c.repository_id ASC, c.id ASC
LIMIT sqlc.arg(query_limit);

-- name: ListControlPlaneAlertsAfter :many
SELECT * FROM control_plane_alerts
WHERE (
    sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text
) AND (
    sqlc.narg(controller)::text IS NULL OR controller = sqlc.narg(controller)::text
)
AND (fired_at, id) < (sqlc.arg(after_fired_at)::timestamptz, sqlc.arg(after_id)::uuid)
ORDER BY fired_at DESC, id DESC
LIMIT sqlc.arg(query_limit);

-- name: ListControlPlaneSilencesAfter :many
SELECT * FROM control_plane_silences
WHERE (created_at, id) < (sqlc.arg(after_created_at)::timestamptz, sqlc.arg(after_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(query_limit);
