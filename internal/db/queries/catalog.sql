-- Helm Repositories

-- name: GetHelmRepositoryByID :one
SELECT * FROM helm_repositories WHERE id = $1;

-- name: GetHelmRepositoryByName :one
SELECT * FROM helm_repositories WHERE name = $1;

-- name: ListHelmRepositories :many
SELECT * FROM helm_repositories ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListEnabledHelmRepositories :many
SELECT * FROM helm_repositories WHERE enabled = true ORDER BY name ASC;

-- name: CreateHelmRepository :one
INSERT INTO helm_repositories (name, url, repo_type, description, is_default, auth_type, auth_config, enabled, created_by_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: UpdateHelmRepository :one
UPDATE helm_repositories SET
    name = $2,
    url = $3,
    repo_type = $4,
    description = $5,
    is_default = $6,
    auth_type = $7,
    auth_config = $8,
    enabled = $9
WHERE id = $1
RETURNING *;

-- name: UpdateHelmRepositoryLastSynced :exec
UPDATE helm_repositories SET last_synced_at = now() WHERE id = $1;

-- name: DeleteHelmRepository :exec
DELETE FROM helm_repositories WHERE id = $1;

-- name: CountHelmRepositories :one
SELECT count(*) FROM helm_repositories;

-- Helm Charts

-- name: GetHelmChartByID :one
SELECT * FROM helm_charts WHERE id = $1;

-- name: GetHelmChartByRepoAndName :one
SELECT * FROM helm_charts WHERE repository_id = $1 AND name = $2;

-- name: ListHelmCharts :many
SELECT * FROM helm_charts ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListChartsByRepository :many
SELECT * FROM helm_charts WHERE repository_id = $1 ORDER BY name ASC LIMIT $2 OFFSET $3;

-- name: ListChartsByCategory :many
SELECT * FROM helm_charts WHERE category = $1 AND deprecated = false ORDER BY name ASC LIMIT $2 OFFSET $3;

-- name: CreateHelmChart :one
INSERT INTO helm_charts (repository_id, name, display_name, description, icon_url, home_url, category, keywords, maintainers, deprecated)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: UpdateHelmChart :one
UPDATE helm_charts SET
    display_name = $2,
    description = $3,
    icon_url = $4,
    home_url = $5,
    category = $6,
    keywords = $7,
    maintainers = $8,
    deprecated = $9
WHERE id = $1
RETURNING *;

-- name: DeleteHelmChart :exec
DELETE FROM helm_charts WHERE id = $1;

-- name: CountHelmCharts :one
SELECT count(*) FROM helm_charts;

-- Helm Chart Versions

-- name: GetHelmChartVersionByID :one
SELECT * FROM helm_chart_versions WHERE id = $1;

-- name: GetHelmChartVersion :one
SELECT * FROM helm_chart_versions WHERE chart_id = $1 AND version = $2;

-- name: GetLatestChartVersion :one
SELECT * FROM helm_chart_versions WHERE chart_id = $1 ORDER BY created_at DESC LIMIT 1;

-- name: ListChartVersions :many
SELECT * FROM helm_chart_versions WHERE chart_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: CreateHelmChartVersion :one
INSERT INTO helm_chart_versions (chart_id, version, app_version, digest, urls, values_schema, default_values, readme, created_at_upstream)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: DeleteHelmChartVersion :exec
DELETE FROM helm_chart_versions WHERE id = $1;

-- name: CountChartVersions :one
SELECT count(*) FROM helm_chart_versions WHERE chart_id = $1;

-- Installed Charts

-- name: GetInstalledChartByID :one
SELECT * FROM installed_charts WHERE id = $1;

-- name: GetInstalledChartByRelease :one
SELECT * FROM installed_charts WHERE cluster_id = $1 AND release_name = $2 AND namespace = $3;

-- name: ListInstalledCharts :many
SELECT * FROM installed_charts ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListInstalledChartsByCluster :many
SELECT * FROM installed_charts WHERE cluster_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListInstalledChartsByToolSlug :many
SELECT * FROM installed_charts WHERE tool_slug = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: CreateInstalledChart :one
INSERT INTO installed_charts (cluster_id, chart_version_id, release_name, namespace, values_override, status, revision, notes, installed_by_id, request_id, tool_slug, preset_used)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: UpdateInstalledChartStatus :exec
UPDATE installed_charts SET status = $2, revision = $3 WHERE id = $1;

-- name: AdoptInstalledChartByRelease :one
UPDATE installed_charts SET
    tool_slug = $4,
    preset_used = $5,
    values_override = $6,
    status = $7,
    revision = $8
WHERE cluster_id = $1 AND release_name = $2 AND namespace = $3
RETURNING *;

-- name: UpdateInstalledChartValues :one
UPDATE installed_charts SET
    values_override = $2,
    status = $3,
    revision = revision + 1
WHERE id = $1
RETURNING *;

-- name: DeleteInstalledChart :exec
DELETE FROM installed_charts WHERE id = $1;

-- name: CountInstalledCharts :one
SELECT count(*) FROM installed_charts;

-- name: CountInstalledChartsByCluster :one
SELECT count(*) FROM installed_charts WHERE cluster_id = $1;
