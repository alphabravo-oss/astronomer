// Hand-written query layer for the compliance-posture rollup
// (internal/handler/compliance_posture.go).
//
// File suffix `_ext` is intentional — same reason as
// catalog_coverage_ext.sql.go: keep hand-written queries out of the
// security.sql.go / image_vulns.sql.go regeneration target so a future
// `sqlc generate` run doesn't drop them. sqlc's CLI is intentionally not
// run in this repo; these are the batched, fleet-wide equivalents of the
// per-cluster queries the posture handler used to fan out N times.

package sqlc

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const listClusterIDsWithSecurityPolicy = `-- name: ListClusterIDsWithSecurityPolicy :many
SELECT DISTINCT cluster_id FROM cluster_security_policies
`

// ListClusterIDsWithSecurityPolicy returns the fleet-wide set of
// cluster_ids that have at least one cluster_security_policies row.
// Unbounded (no LIMIT/OFFSET) so a cluster whose policy would sort past a
// paged window is never mis-scored.
func (q *Queries) ListClusterIDsWithSecurityPolicy(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := q.db.Query(ctx, listClusterIDsWithSecurityPolicy)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []uuid.UUID{}
	for rows.Next() {
		var clusterID uuid.UUID
		if err := rows.Scan(&clusterID); err != nil {
			return nil, err
		}
		items = append(items, clusterID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const latestCISScanPerCluster = `-- name: LatestCISScanPerCluster :many
SELECT DISTINCT ON (cluster_id)
    cluster_id,
    passed,
    failed,
    completed_at
FROM security_scan_results
WHERE scan_type = 'cis'
ORDER BY cluster_id, created_at DESC
`

// LatestCISScanPerClusterRow is the slim projection the posture rollup
// needs: the newest CIS scan's pass/fail counts and completion time per
// cluster.
type LatestCISScanPerClusterRow struct {
	ClusterID   uuid.UUID          `json:"cluster_id"`
	Passed      int32              `json:"passed"`
	Failed      int32              `json:"failed"`
	CompletedAt pgtype.Timestamptz `json:"completed_at"`
}

// LatestCISScanPerCluster returns the latest CIS scan (by created_at) for
// every cluster that has one, batched across the whole fleet.
func (q *Queries) LatestCISScanPerCluster(ctx context.Context) ([]LatestCISScanPerClusterRow, error) {
	rows, err := q.db.Query(ctx, latestCISScanPerCluster)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []LatestCISScanPerClusterRow{}
	for rows.Next() {
		var i LatestCISScanPerClusterRow
		if err := rows.Scan(
			&i.ClusterID,
			&i.Passed,
			&i.Failed,
			&i.CompletedAt,
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

const aggregateVulnerabilitiesPerCluster = `-- name: AggregateVulnerabilitiesPerCluster :many
SELECT
    cluster_id,
    COALESCE(SUM(critical_count), 0)::bigint AS critical,
    COALESCE(SUM(high_count), 0)::bigint AS high,
    COUNT(*)::bigint AS report_count
FROM image_vulnerability_reports
GROUP BY cluster_id
`

// AggregateVulnerabilitiesPerClusterRow mirrors the per-cluster shape of
// AggregateClusterVulnerabilitiesRow but only the fields the posture
// rollup scores on, keyed by cluster_id.
type AggregateVulnerabilitiesPerClusterRow struct {
	ClusterID   uuid.UUID `json:"cluster_id"`
	Critical    int64     `json:"critical"`
	High        int64     `json:"high"`
	ReportCount int64     `json:"report_count"`
}

// AggregateVulnerabilitiesPerCluster returns per-cluster critical/high/
// report_count aggregates for the whole fleet in one pass.
func (q *Queries) AggregateVulnerabilitiesPerCluster(ctx context.Context) ([]AggregateVulnerabilitiesPerClusterRow, error) {
	rows, err := q.db.Query(ctx, aggregateVulnerabilitiesPerCluster)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AggregateVulnerabilitiesPerClusterRow{}
	for rows.Next() {
		var i AggregateVulnerabilitiesPerClusterRow
		if err := rows.Scan(
			&i.ClusterID,
			&i.Critical,
			&i.High,
			&i.ReportCount,
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
