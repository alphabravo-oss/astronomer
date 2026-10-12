-- Candidate selection precedes every residual predicate. This MATERIALIZED
-- boundary and per-key LATERAL LIMIT retain bounded lookup plans in generic
-- and custom preparation; actual EXPLAIN ANALYZE remains the authority.

-- name: ListCatalogGlobalCandidateWindowFirst :many
WITH candidates AS MATERIALIZED (
    SELECT key.name,key.repository_id,key.chart_id
    FROM public.catalog_global_chart_keys key
    ORDER BY key.name ASC,key.repository_id ASC,key.chart_id ASC
    LIMIT sqlc.arg(candidate_limit)::int
)
SELECT chart.*,
       ((sqlc.arg(tag)::text='' OR tagged.chart_id IS NOT NULL)
        AND (sqlc.arg(search_pattern)::text='' OR chart.name ILIKE sqlc.arg(search_pattern)::text ESCAPE '\'
             OR chart.display_name ILIKE sqlc.arg(search_pattern)::text ESCAPE '\'
             OR chart.description ILIKE sqlc.arg(search_pattern)::text ESCAPE '\'))::boolean AS matches
FROM candidates
JOIN LATERAL (
    SELECT c.* FROM public.helm_charts c
    WHERE c.id=candidates.chart_id AND c.repository_id=candidates.repository_id AND c.name=candidates.name
    LIMIT 1
) chart ON true
JOIN LATERAL (
    SELECT r.id FROM public.helm_repositories r
    WHERE r.id=candidates.repository_id AND r.owner_project_id IS NULL
    LIMIT 1
) repository ON true
LEFT JOIN LATERAL (
    SELECT tag.chart_id FROM public.helm_chart_tags tag
    WHERE sqlc.arg(tag)::text<>'' AND tag.chart_id=candidates.chart_id AND tag.tag=sqlc.arg(tag)::text
    LIMIT 1
) tagged ON true
ORDER BY candidates.name ASC,candidates.repository_id ASC,candidates.chart_id ASC;

-- name: ListCatalogGlobalCandidateWindowAfter :many
WITH candidates AS MATERIALIZED (
    SELECT key.name,key.repository_id,key.chart_id
    FROM public.catalog_global_chart_keys key
    WHERE (key.name,key.repository_id,key.chart_id) > (sqlc.arg(after_name)::varchar,sqlc.arg(after_repository_id)::uuid,sqlc.arg(after_id)::uuid)
    ORDER BY key.name ASC,key.repository_id ASC,key.chart_id ASC
    LIMIT sqlc.arg(candidate_limit)::int
)
SELECT chart.*,
       ((sqlc.arg(tag)::text='' OR tagged.chart_id IS NOT NULL)
        AND (sqlc.arg(search_pattern)::text='' OR chart.name ILIKE sqlc.arg(search_pattern)::text ESCAPE '\'
             OR chart.display_name ILIKE sqlc.arg(search_pattern)::text ESCAPE '\'
             OR chart.description ILIKE sqlc.arg(search_pattern)::text ESCAPE '\'))::boolean AS matches
FROM candidates
JOIN LATERAL (
    SELECT c.* FROM public.helm_charts c
    WHERE c.id=candidates.chart_id AND c.repository_id=candidates.repository_id AND c.name=candidates.name
    LIMIT 1
) chart ON true
JOIN LATERAL (
    SELECT r.id FROM public.helm_repositories r
    WHERE r.id=candidates.repository_id AND r.owner_project_id IS NULL
    LIMIT 1
) repository ON true
LEFT JOIN LATERAL (
    SELECT tag.chart_id FROM public.helm_chart_tags tag
    WHERE sqlc.arg(tag)::text<>'' AND tag.chart_id=candidates.chart_id AND tag.tag=sqlc.arg(tag)::text
    LIMIT 1
) tagged ON true
ORDER BY candidates.name ASC,candidates.repository_id ASC,candidates.chart_id ASC;
