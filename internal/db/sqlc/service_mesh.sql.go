// Migration 071 — service mesh detection state.
//
// Hand-authored sqlc shim. The repo's sqlc generator is occasionally
// not runnable in agent worktrees (it talks to an external binary);
// this file mirrors what sqlc would produce so a future `make sqlc`
// run is a no-op. Same pattern as 053_cloud_credentials.

package sqlc

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ClusterServiceMesh mirrors the cluster_service_mesh row. One row per
// cluster; populated by the mesh:detect worker on a 5m cadence and on
// the on-demand POST .../service-mesh/detect/ handler.
type ClusterServiceMesh struct {
	ClusterID               uuid.UUID          `json:"cluster_id"`
	DetectedMesh            string             `json:"detected_mesh"`
	DetectedVersion         string             `json:"detected_version"`
	ControlPlaneNamespace   string             `json:"control_plane_namespace"`
	GatewayCount            int32              `json:"gateway_count"`
	VirtualServiceCount     int32              `json:"virtual_service_count"`
	DestinationRuleCount    int32              `json:"destination_rule_count"`
	PeerAuthenticationCount int32              `json:"peer_authentication_count"`
	ServiceProfileCount     int32              `json:"service_profile_count"`
	ServerAuthCount         int32              `json:"server_auth_count"`
	MtlsCoveragePct         int32              `json:"mtls_coverage_pct"`
	LastDetectedAt          pgtype.Timestamptz `json:"last_detected_at"`
	LastError               string             `json:"last_error"`
	CreatedAt               pgtype.Timestamptz `json:"created_at"`
	UpdatedAt               pgtype.Timestamptz `json:"updated_at"`
}

const clusterServiceMeshColumns = `cluster_id, detected_mesh, detected_version, control_plane_namespace,
    gateway_count, virtual_service_count, destination_rule_count,
    peer_authentication_count, service_profile_count, server_auth_count,
    mtls_coverage_pct, last_detected_at, last_error, created_at, updated_at`

func scanClusterServiceMeshRow(row interface {
	Scan(dest ...any) error
}) (ClusterServiceMesh, error) {
	var i ClusterServiceMesh
	err := row.Scan(
		&i.ClusterID,
		&i.DetectedMesh,
		&i.DetectedVersion,
		&i.ControlPlaneNamespace,
		&i.GatewayCount,
		&i.VirtualServiceCount,
		&i.DestinationRuleCount,
		&i.PeerAuthenticationCount,
		&i.ServiceProfileCount,
		&i.ServerAuthCount,
		&i.MtlsCoveragePct,
		&i.LastDetectedAt,
		&i.LastError,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const getClusterServiceMesh = `-- name: GetClusterServiceMesh :one
SELECT ` + clusterServiceMeshColumns + `
FROM cluster_service_mesh WHERE cluster_id = $1`

// GetClusterServiceMesh returns the detection row for one cluster. When
// no detection has run, the caller receives pgx.ErrNoRows — the handler
// translates that into the empty/"unknown" response so the UI can
// render a stable "no data yet" state.
func (q *Queries) GetClusterServiceMesh(ctx context.Context, clusterID uuid.UUID) (ClusterServiceMesh, error) {
	row := q.db.QueryRow(ctx, getClusterServiceMesh, clusterID)
	return scanClusterServiceMeshRow(row)
}

const listClusterServiceMesh = `-- name: ListClusterServiceMesh :many
SELECT ` + clusterServiceMeshColumns + `
FROM cluster_service_mesh ORDER BY cluster_id ASC`

// ListClusterServiceMesh is used by the fleet metrics exporter that
// emits astronomer_cluster_mesh{cluster,mesh} = 1.
func (q *Queries) ListClusterServiceMesh(ctx context.Context) ([]ClusterServiceMesh, error) {
	rows, err := q.db.Query(ctx, listClusterServiceMesh)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ClusterServiceMesh{}
	for rows.Next() {
		i, err := scanClusterServiceMeshRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const upsertClusterServiceMesh = `-- name: UpsertClusterServiceMesh :one
INSERT INTO cluster_service_mesh (
    cluster_id, detected_mesh, detected_version, control_plane_namespace,
    gateway_count, virtual_service_count, destination_rule_count,
    peer_authentication_count, service_profile_count, server_auth_count,
    mtls_coverage_pct, last_detected_at, last_error
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, now(), $12)
ON CONFLICT (cluster_id) DO UPDATE SET
    detected_mesh             = EXCLUDED.detected_mesh,
    detected_version          = EXCLUDED.detected_version,
    control_plane_namespace   = EXCLUDED.control_plane_namespace,
    gateway_count             = EXCLUDED.gateway_count,
    virtual_service_count     = EXCLUDED.virtual_service_count,
    destination_rule_count    = EXCLUDED.destination_rule_count,
    peer_authentication_count = EXCLUDED.peer_authentication_count,
    service_profile_count     = EXCLUDED.service_profile_count,
    server_auth_count         = EXCLUDED.server_auth_count,
    mtls_coverage_pct         = EXCLUDED.mtls_coverage_pct,
    last_detected_at          = now(),
    last_error                = EXCLUDED.last_error,
    updated_at                = now()
RETURNING ` + clusterServiceMeshColumns

// UpsertClusterServiceMeshParams is the param shape for the detection
// upsert. last_detected_at + updated_at are set to now() server-side so
// the caller doesn't need a clock.
type UpsertClusterServiceMeshParams struct {
	ClusterID               uuid.UUID `json:"cluster_id"`
	DetectedMesh            string    `json:"detected_mesh"`
	DetectedVersion         string    `json:"detected_version"`
	ControlPlaneNamespace   string    `json:"control_plane_namespace"`
	GatewayCount            int32     `json:"gateway_count"`
	VirtualServiceCount     int32     `json:"virtual_service_count"`
	DestinationRuleCount    int32     `json:"destination_rule_count"`
	PeerAuthenticationCount int32     `json:"peer_authentication_count"`
	ServiceProfileCount     int32     `json:"service_profile_count"`
	ServerAuthCount         int32     `json:"server_auth_count"`
	MtlsCoveragePct         int32     `json:"mtls_coverage_pct"`
	LastError               string    `json:"last_error"`
}

// UpsertClusterServiceMesh inserts or updates the per-cluster detection
// row. The worker calls this once per detection cycle; the handler
// calls it via the worker for on-demand detect requests.
func (q *Queries) UpsertClusterServiceMesh(ctx context.Context, arg UpsertClusterServiceMeshParams) (ClusterServiceMesh, error) {
	row := q.db.QueryRow(ctx, upsertClusterServiceMesh,
		arg.ClusterID,
		arg.DetectedMesh,
		arg.DetectedVersion,
		arg.ControlPlaneNamespace,
		arg.GatewayCount,
		arg.VirtualServiceCount,
		arg.DestinationRuleCount,
		arg.PeerAuthenticationCount,
		arg.ServiceProfileCount,
		arg.ServerAuthCount,
		arg.MtlsCoveragePct,
		arg.LastError,
	)
	return scanClusterServiceMeshRow(row)
}

const deleteClusterServiceMesh = `-- name: DeleteClusterServiceMesh :exec
DELETE FROM cluster_service_mesh WHERE cluster_id = $1`

// DeleteClusterServiceMesh is reserved for the cluster-decommission
// path; the CASCADE on the FK does the same thing automatically, but
// having an explicit verb keeps the handler symmetric with the rest
// of the cluster sub-resources.
func (q *Queries) DeleteClusterServiceMesh(ctx context.Context, clusterID uuid.UUID) error {
	_, err := q.db.Exec(ctx, deleteClusterServiceMesh, clusterID)
	return err
}

// ListHelmChartsByTag lists charts carrying a tag in helm_chart_tags.
// Used by the catalog `?tag=` filter so the "Install" button in the
// service-mesh tab can deep-link to the catalog filtered to mesh
// charts.
const listHelmChartsByTag = `-- name: ListHelmChartsByTag :many
SELECT c.id, c.repository_id, c.name, c.display_name, c.description, c.icon_url,
       c.home_url, c.category, c.keywords, c.maintainers, c.deprecated,
       c.created_at, c.updated_at
FROM helm_charts c
JOIN helm_chart_tags t ON t.chart_id = c.id
WHERE t.tag = $1
ORDER BY c.name ASC
LIMIT $2 OFFSET $3`

// ListHelmChartsByTagParams is the param shape for the tag-filtered
// catalog listing.
type ListHelmChartsByTagParams struct {
	Tag    string `json:"tag"`
	Limit  int32  `json:"limit"`
	Offset int32  `json:"offset"`
}

// ListHelmChartsByTag returns charts joined to helm_chart_tags. Schema
// of the returned row mirrors HelmChart in catalog.sql.go.
func (q *Queries) ListHelmChartsByTag(ctx context.Context, arg ListHelmChartsByTagParams) ([]HelmChart, error) {
	rows, err := q.db.Query(ctx, listHelmChartsByTag, arg.Tag, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []HelmChart{}
	for rows.Next() {
		var i HelmChart
		if err := rows.Scan(
			&i.ID,
			&i.RepositoryID,
			&i.Name,
			&i.DisplayName,
			&i.Description,
			&i.IconUrl,
			&i.HomeUrl,
			&i.Category,
			&i.Keywords,
			&i.Maintainers,
			&i.Deprecated,
			&i.CreatedAt,
			&i.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const countHelmChartsByTag = `-- name: CountHelmChartsByTag :one
SELECT COUNT(*) FROM helm_charts c
JOIN helm_chart_tags t ON t.chart_id = c.id
WHERE t.tag = $1`

// CountHelmChartsByTag is the companion count for ListHelmChartsByTag —
// drives the pagination total in the catalog response envelope.
func (q *Queries) CountHelmChartsByTag(ctx context.Context, tag string) (int64, error) {
	row := q.db.QueryRow(ctx, countHelmChartsByTag, tag)
	var n int64
	err := row.Scan(&n)
	return n, err
}
