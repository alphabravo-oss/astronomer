-- Helm Repositories

-- name: GetHelmRepositoryByID :one
SELECT * FROM helm_repositories WHERE id = $1;

-- name: ListHelmRepositories :many
SELECT * FROM helm_repositories ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListGlobalHelmRepositories :many
-- Admin default catalog view: only operator-curated global catalogs
-- (owner_project_id IS NULL). Filtering + paginating at the DB layer
-- avoids the over-fetch/in-Go-filter dance that dropped globals past the
-- slack window and emitted empty trailing pages once private rows were
-- excluded.
SELECT * FROM helm_repositories
WHERE owner_project_id IS NULL
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountGlobalHelmRepositories :one
SELECT count(*) FROM helm_repositories WHERE owner_project_id IS NULL;

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

-- name: ListChartsByRepositoryIDs :many
-- Project-scoped catalog browse: charts across the project's visible
-- catalog set in a single query with real LIMIT/OFFSET, instead of
-- fanning out a Limit:1000 query per catalog and slicing in Go (which
-- silently truncated any catalog with >1000 charts).
SELECT * FROM helm_charts
WHERE repository_id = ANY(sqlc.arg(repository_ids)::uuid[])
ORDER BY name ASC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountChartsByRepositoryIDs :one
SELECT count(*) FROM helm_charts
WHERE repository_id = ANY(sqlc.arg(repository_ids)::uuid[]);

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

-- name: ListChartVersionStrings :many
-- Lightweight probe used by the repo-index ingest path to bulk-load the
-- set of already-known version strings for a chart in a single round
-- trip, replacing the per-version SELECT probe.
SELECT version FROM helm_chart_versions WHERE chart_id = $1;

-- name: BulkCreateHelmChartVersions :many
-- Multi-row insert for repo-index ingest. Rows arrive as a JSON array
-- (jsonb_to_recordset) so a whole chart's versions land in one query
-- instead of one INSERT per version. ON CONFLICT DO NOTHING makes the
-- ingest idempotent against concurrent syncs; RETURNING version yields
-- exactly the rows that were newly inserted so the caller can count them.
INSERT INTO helm_chart_versions (chart_id, version, app_version, digest, urls, created_at_upstream)
SELECT
    sqlc.arg(chart_id)::uuid,
    x.version,
    x.app_version,
    x.digest,
    x.urls,
    x.created_at_upstream
FROM jsonb_to_recordset(sqlc.arg(rows)::jsonb) AS x(
    version text,
    app_version text,
    digest text,
    urls jsonb,
    created_at_upstream timestamptz
)
ON CONFLICT (chart_id, version) DO NOTHING
RETURNING version;

-- name: DeleteHelmChartVersion :exec
DELETE FROM helm_chart_versions WHERE id = $1;

-- Installed Charts

-- name: GetInstalledChartByID :one
SELECT * FROM installed_charts WHERE id = $1;

-- name: GetInstalledChartByRelease :one
SELECT * FROM installed_charts WHERE cluster_id = $1 AND release_name = $2 AND namespace = $3;

-- name: GetInstalledChartByClusterAndTool :one
-- Indexed lookup (cluster_id + tool_slug) for the duplicate-install guard.
-- Replaces the previous first-200-row in-Go scan, which missed the 409
-- when a cluster had more than 200 installed charts before the duplicate.
SELECT * FROM installed_charts
WHERE cluster_id = sqlc.arg(cluster_id)::uuid
  AND tool_slug = sqlc.arg(tool_slug)::text
ORDER BY created_at DESC
LIMIT 1;

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

-- name: ListInstalledChartsForDriftSweep :many
-- Active installed charts the tool-drift sweep probes against their live
-- helm release. Only rows that are supposed to be deployed (not mid-install
-- or already removed) are worth comparing.
SELECT * FROM installed_charts
WHERE status IN ('installed', 'deployed', 'upgraded')
ORDER BY drift_checked_at ASC NULLS FIRST, updated_at ASC
LIMIT $1;

-- name: MarkInstalledChartDrift :exec
UPDATE installed_charts SET
    drift_detected = $2,
    drift_detail = $3,
    drift_checked_at = now()
WHERE id = $1;

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
    revision = $4
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
