-- Helm Repositories

-- name: GetHelmRepositoryByID :one
SELECT * FROM helm_repositories WHERE id = $1;

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

-- name: CreateHelmChart :one
INSERT INTO helm_charts (repository_id, name, display_name, description, icon_url, home_url, category, keywords, maintainers, deprecated)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
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
-- Orders by the upstream chart's publish time (the `created:` field
-- in helm index.yaml, persisted to created_at_upstream during ingest)
-- so the install modal's "default to latest" picks the actual newest
-- version rather than whichever row happened to be inserted last
-- during a backfill sync. Falls back to created_at DESC when the
-- upstream timestamp is NULL (older catalog rows pre-dating the
-- created_at_upstream column or OCI charts without a publish date).
SELECT * FROM helm_chart_versions
WHERE chart_id = $1
ORDER BY created_at_upstream DESC NULLS LAST, created_at DESC
LIMIT 1;

-- name: ListChartVersions :many
-- Same ordering rationale as GetLatestChartVersion — the version
-- dropdown in the install/upgrade modal needs newest-first by upstream
-- publish time, not by DB insert order.
SELECT * FROM helm_chart_versions
WHERE chart_id = $1
ORDER BY created_at_upstream DESC NULLS LAST, created_at DESC
LIMIT $2 OFFSET $3;

-- name: CreateHelmChartVersion :one
INSERT INTO helm_chart_versions (chart_id, version, app_version, digest, urls, values_schema, default_values, readme, created_at_upstream)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: DeleteHelmChartVersion :exec
DELETE FROM helm_chart_versions WHERE id = $1;

-- Installed Charts

-- name: GetInstalledChartByID :one
SELECT * FROM installed_charts WHERE id = $1;

-- name: GetInstalledChartByRelease :one
SELECT * FROM installed_charts WHERE cluster_id = $1 AND release_name = $2 AND namespace = $3;

-- name: ListInstalledCharts :many
SELECT * FROM installed_charts ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListInstalledChartsByCluster :many
SELECT * FROM installed_charts WHERE cluster_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

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

-- name: DeleteFailedInstallationsByCluster :execrows
-- Hard-deletes installed_charts rows that are stuck in a failed_* state
-- on the given cluster. Used by the Apps tab's "Delete failed installs"
-- bulk action; mirrors Rancher's "Force Delete" affordance for orphaned
-- helm release rows that didn't actually deploy (and therefore can't be
-- uninstalled cleanly). Returns the affected-row count so the handler
-- can include it in the response.
DELETE FROM installed_charts
WHERE cluster_id = $1
  AND status IN ('failed_install', 'failed_uninstall');
