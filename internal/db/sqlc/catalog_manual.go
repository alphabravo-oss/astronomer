// Sprint 082 — catalog Apps-tab support queries.
//
// Hand-written because the sqlc CLI is broken on compliance.sql's
// lexer (see SPRINT_PROGRESS.md). Mirrors the generated style so the
// receiving handler code can use these like any other Querier method.
//
// Two queries:
//
//   UpdateHelmChartVersionContent — lazy-hydration writeback for
//     default_values + readme. The ingest path leaves these empty to
//     keep the catalog sync fast; the GetChartValues/GetChartReadme
//     endpoints call this on cache miss after pulling + parsing the
//     chart tarball.
//
//   ListInstalledChartsWithMetadataByCluster — joined view that the
//     Apps tab list renders against. LEFT JOINs against
//     helm_chart_versions → helm_charts → helm_repositories so the UI
//     gets chart name / icon / version string without N+1 fetches.
//     The LEFT JOIN is deliberate: Tools-installed rows have
//     chart_version_id = NULL and identify themselves via tool_slug,
//     and we want those to appear in the list (with "Managed by
//     Tools" pivot) rather than be silently dropped.

package sqlc

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// -- UpdateHelmChartVersionContent ------------------------------------

const updateHelmChartVersionContent = `-- name: UpdateHelmChartVersionContent :exec
UPDATE helm_chart_versions
SET default_values = $2,
    readme         = $3,
    updated_at     = now()
WHERE id = $1
`

// UpdateHelmChartVersionContentParams is what the hydrate path writes
// back after loading the chart archive. Schema fields kept text-typed
// because helm's values.yaml + README.md are large free-form strings;
// no need for JSON validation here.
type UpdateHelmChartVersionContentParams struct {
	ID            uuid.UUID `json:"id"`
	DefaultValues string    `json:"default_values"`
	Readme        string    `json:"readme"`
}

// UpdateHelmChartVersionContent persists hydrated values.yaml + README
// onto an existing helm_chart_versions row. Best-effort: failures here
// are logged but don't fail the handler — the in-memory hydration
// still serves the request, the row just stays empty for next time.
func (q *Queries) UpdateHelmChartVersionContent(ctx context.Context, arg UpdateHelmChartVersionContentParams) error {
	_, err := q.db.Exec(ctx, updateHelmChartVersionContent,
		arg.ID, arg.DefaultValues, arg.Readme,
	)
	return err
}

// -- ListInstalledChartsWithMetadataByCluster -------------------------

const listInstalledChartsWithMetadataByCluster = `-- name: ListInstalledChartsWithMetadataByCluster :many
SELECT
    ic.id, ic.cluster_id, ic.chart_version_id, ic.release_name, ic.namespace,
    ic.values_override, ic.status, ic.revision, ic.notes, ic.installed_by_id,
    ic.request_id, ic.tool_slug, ic.preset_used, ic.created_at, ic.updated_at,
    hcv.version           AS chart_version,
    hcv.app_version       AS chart_app_version,
    hc.id                 AS chart_id,
    hc.name               AS chart_name,
    hc.display_name       AS chart_display_name,
    hc.description        AS chart_description,
    hc.icon_url           AS chart_icon_url,
    hc.category           AS chart_category,
    hr.name               AS repo_name,
    hr.repo_type          AS repo_type
FROM installed_charts ic
LEFT JOIN helm_chart_versions hcv ON ic.chart_version_id = hcv.id
LEFT JOIN helm_charts hc          ON hcv.chart_id = hc.id
LEFT JOIN helm_repositories hr    ON hc.repository_id = hr.id
WHERE ic.cluster_id = $1
ORDER BY ic.created_at DESC
LIMIT $2 OFFSET $3
`

// ListInstalledChartsWithMetadataByClusterParams: cluster + paging.
type ListInstalledChartsWithMetadataByClusterParams struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	Limit     int32     `json:"limit"`
	Offset    int32     `json:"offset"`
}

// InstalledChartWithMetadata is the enriched row shape — base
// installed_charts columns plus nullable chart/repo metadata. Tools
// installs leave the chart_* columns null; the UI uses tool_slug as
// the display fallback in that case.
type InstalledChartWithMetadata struct {
	InstalledChart
	ChartVersion     pgtype.Text `json:"chart_version"`
	ChartAppVersion  pgtype.Text `json:"chart_app_version"`
	ChartID          pgtype.UUID `json:"chart_id"`
	ChartName        pgtype.Text `json:"chart_name"`
	ChartDisplayName pgtype.Text `json:"chart_display_name"`
	ChartDescription pgtype.Text `json:"chart_description"`
	ChartIconUrl     pgtype.Text `json:"chart_icon_url"`
	ChartCategory    pgtype.Text `json:"chart_category"`
	RepoName         pgtype.Text `json:"repo_name"`
	RepoType         pgtype.Text `json:"repo_type"`
}

// ListInstalledChartsWithMetadataByCluster powers the Apps tab list
// view. Returns every installed_charts row for the cluster with chart
// metadata when available; Tools installs (chart_version_id NULL)
// come back with empty chart_* fields and the caller falls back to
// tool_slug.
func (q *Queries) ListInstalledChartsWithMetadataByCluster(ctx context.Context, arg ListInstalledChartsWithMetadataByClusterParams) ([]InstalledChartWithMetadata, error) {
	rows, err := q.db.Query(ctx, listInstalledChartsWithMetadataByCluster, arg.ClusterID, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []InstalledChartWithMetadata{}
	for rows.Next() {
		var i InstalledChartWithMetadata
		if err := rows.Scan(
			&i.ID, &i.ClusterID, &i.ChartVersionID, &i.ReleaseName, &i.Namespace,
			&i.ValuesOverride, &i.Status, &i.Revision, &i.Notes, &i.InstalledByID,
			&i.RequestID, &i.ToolSlug, &i.PresetUsed, &i.CreatedAt, &i.UpdatedAt,
			&i.ChartVersion, &i.ChartAppVersion,
			&i.ChartID,
			&i.ChartName, &i.ChartDisplayName, &i.ChartDescription,
			&i.ChartIconUrl, &i.ChartCategory,
			&i.RepoName, &i.RepoType,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}
