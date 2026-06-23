-- Blessed-chart catalog overlays (sourced from astronomer-catalog/catalog.yaml).

-- name: UpsertDefaultHelmRepository :exec
-- Seed/refresh a platform-default repo. On conflict we update the URL/description
-- and re-assert is_default, but deliberately leave `enabled` alone so an operator
-- who disabled a default repo keeps it disabled across reconciles.
INSERT INTO helm_repositories (name, url, repo_type, description, is_default, enabled)
VALUES ($1, $2, 'helm', $3, true, true)
ON CONFLICT (name) DO UPDATE SET
    url = EXCLUDED.url,
    description = EXCLUDED.description,
    is_default = true,
    updated_at = now();

-- name: DeleteBlessedChartsBySource :exec
DELETE FROM catalog_blessed_charts WHERE source = $1;

-- name: CreateBlessedChart :exec
INSERT INTO catalog_blessed_charts
    (repo_url, chart_name, display_name, description, category, icon_url, mgmt_safe, version_policy, source)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: ListBlessedCharts :many
SELECT * FROM catalog_blessed_charts ORDER BY category ASC, chart_name ASC;

-- name: GetBlessedChart :one
SELECT * FROM catalog_blessed_charts WHERE repo_url = $1 AND chart_name = $2;

-- name: CountBlessedCharts :one
SELECT count(*) FROM catalog_blessed_charts;
