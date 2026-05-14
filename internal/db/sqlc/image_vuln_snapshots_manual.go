// Sprint 081 — image vulnerability scan snapshot queries.
//
// Hand-written because the sqlc CLI is broken on
// compliance.sql's lexer (see SPRINT_PROGRESS.md). We mirror the
// generated style (one var per SQL string, typed params struct,
// method on *Queries) so this file is indistinguishable from a sqlc
// emitter's output for the receiving handler/registry code.
//
// Three queries:
//
//   InsertImageVulnerabilityReportSnapshot — appended on every Trivy
//     ingest. ON CONFLICT (report_id, scanned_at) DO NOTHING so a
//     mirror-event replay (which the agent does after reconnect)
//     doesn't double-insert.
//
//   ListImageVulnerabilityHistoryForCluster — sparkline data for the
//     cluster-wide trend card. Buckets are at the (cluster, scanned_at)
//     granularity; UI groups them client-side.
//
//   ListImageVulnerabilityHistoryForReport — per-image timeline for
//     the "what changed for this image" drawer.
//
//   PruneOldImageVulnerabilityReportSnapshots — retention sweep,
//     90d default.

package sqlc

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// -- InsertImageVulnerabilityReportSnapshot ---------------------------

const insertImageVulnerabilityReportSnapshot = `-- name: InsertImageVulnerabilityReportSnapshot :exec
INSERT INTO image_vulnerability_report_snapshots (
    report_id, cluster_id,
    critical_count, high_count, medium_count, low_count, unknown_count,
    scanned_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (report_id, scanned_at) DO NOTHING
`

// InsertImageVulnerabilityReportSnapshotParams binds the values an
// ingester writes per Trivy event.
type InsertImageVulnerabilityReportSnapshotParams struct {
	ReportID      uuid.UUID          `json:"report_id"`
	ClusterID     uuid.UUID          `json:"cluster_id"`
	CriticalCount int32              `json:"critical_count"`
	HighCount     int32              `json:"high_count"`
	MediumCount   int32              `json:"medium_count"`
	LowCount      int32              `json:"low_count"`
	UnknownCount  int32              `json:"unknown_count"`
	ScannedAt     pgtype.Timestamptz `json:"scanned_at"`
}

// InsertImageVulnerabilityReportSnapshot appends one snapshot. The
// ON CONFLICT clause makes this idempotent so the scanner.Ingester
// can call it inside the same transaction as the upsert without
// worrying about replay races.
func (q *Queries) InsertImageVulnerabilityReportSnapshot(ctx context.Context, arg InsertImageVulnerabilityReportSnapshotParams) error {
	_, err := q.db.Exec(ctx, insertImageVulnerabilityReportSnapshot,
		arg.ReportID, arg.ClusterID,
		arg.CriticalCount, arg.HighCount, arg.MediumCount, arg.LowCount, arg.UnknownCount,
		arg.ScannedAt,
	)
	return err
}

// -- ListImageVulnerabilityHistoryForCluster --------------------------

const listImageVulnerabilityHistoryForCluster = `-- name: ListImageVulnerabilityHistoryForCluster :many
SELECT
    scanned_at,
    SUM(critical_count)::INTEGER AS critical,
    SUM(high_count)::INTEGER     AS high,
    SUM(medium_count)::INTEGER   AS medium,
    SUM(low_count)::INTEGER      AS low,
    SUM(unknown_count)::INTEGER  AS unknown,
    COUNT(*)::INTEGER            AS report_count
FROM image_vulnerability_report_snapshots
WHERE cluster_id = $1
  AND scanned_at >= $2
GROUP BY scanned_at
ORDER BY scanned_at DESC
LIMIT $3
`

// ListImageVulnerabilityHistoryForClusterParams: cluster_id, since
// (lower bound), limit. The limit caps the sparkline at a sensible
// width without paging — anything older than `since` is filtered out
// before the GROUP BY rolls each scan time up.
type ListImageVulnerabilityHistoryForClusterParams struct {
	ClusterID uuid.UUID          `json:"cluster_id"`
	Since     pgtype.Timestamptz `json:"since"`
	Limit     int32              `json:"limit"`
}

// ListImageVulnerabilityHistoryForClusterRow is one trend-line point.
// Sum'd across reports so the UI sees a single "total criticals at
// this scan time" number per row, not per (image, time) cross-join.
type ListImageVulnerabilityHistoryForClusterRow struct {
	ScannedAt   pgtype.Timestamptz `json:"scanned_at"`
	Critical    int32              `json:"critical"`
	High        int32              `json:"high"`
	Medium      int32              `json:"medium"`
	Low         int32              `json:"low"`
	Unknown     int32              `json:"unknown"`
	ReportCount int32              `json:"report_count"`
}

func (q *Queries) ListImageVulnerabilityHistoryForCluster(ctx context.Context, arg ListImageVulnerabilityHistoryForClusterParams) ([]ListImageVulnerabilityHistoryForClusterRow, error) {
	rows, err := q.db.Query(ctx, listImageVulnerabilityHistoryForCluster, arg.ClusterID, arg.Since, arg.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ListImageVulnerabilityHistoryForClusterRow{}
	for rows.Next() {
		var i ListImageVulnerabilityHistoryForClusterRow
		if err := rows.Scan(&i.ScannedAt, &i.Critical, &i.High, &i.Medium, &i.Low, &i.Unknown, &i.ReportCount); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

// -- ListImageVulnerabilityHistoryForReport ---------------------------

const listImageVulnerabilityHistoryForReport = `-- name: ListImageVulnerabilityHistoryForReport :many
SELECT
    id, report_id, cluster_id,
    critical_count, high_count, medium_count, low_count, unknown_count,
    scanned_at, created_at
FROM image_vulnerability_report_snapshots
WHERE report_id = $1
ORDER BY scanned_at DESC
LIMIT $2
`

type ListImageVulnerabilityHistoryForReportParams struct {
	ReportID uuid.UUID `json:"report_id"`
	Limit    int32     `json:"limit"`
}

// ImageVulnerabilityReportSnapshot is the table row shape.
type ImageVulnerabilityReportSnapshot struct {
	ID            uuid.UUID          `json:"id"`
	ReportID      uuid.UUID          `json:"report_id"`
	ClusterID     uuid.UUID          `json:"cluster_id"`
	CriticalCount int32              `json:"critical_count"`
	HighCount     int32              `json:"high_count"`
	MediumCount   int32              `json:"medium_count"`
	LowCount      int32              `json:"low_count"`
	UnknownCount  int32              `json:"unknown_count"`
	ScannedAt     pgtype.Timestamptz `json:"scanned_at"`
	CreatedAt     pgtype.Timestamptz `json:"created_at"`
}

func (q *Queries) ListImageVulnerabilityHistoryForReport(ctx context.Context, arg ListImageVulnerabilityHistoryForReportParams) ([]ImageVulnerabilityReportSnapshot, error) {
	rows, err := q.db.Query(ctx, listImageVulnerabilityHistoryForReport, arg.ReportID, arg.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ImageVulnerabilityReportSnapshot{}
	for rows.Next() {
		var i ImageVulnerabilityReportSnapshot
		if err := rows.Scan(&i.ID, &i.ReportID, &i.ClusterID,
			&i.CriticalCount, &i.HighCount, &i.MediumCount, &i.LowCount, &i.UnknownCount,
			&i.ScannedAt, &i.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

// -- Diff: latest vs N hours ago --------------------------------------

const compareImageVulnerabilitySnapshotsForCluster = `-- name: CompareImageVulnerabilitySnapshotsForCluster :one
WITH latest AS (
    SELECT
        SUM(critical_count)::INTEGER AS critical,
        SUM(high_count)::INTEGER AS high,
        SUM(medium_count)::INTEGER AS medium,
        SUM(low_count)::INTEGER AS low,
        SUM(unknown_count)::INTEGER AS unknown,
        MAX(scanned_at) AS scanned_at
    FROM image_vulnerability_report_snapshots
    WHERE cluster_id = $1
      AND scanned_at = (
          SELECT MAX(scanned_at)
          FROM image_vulnerability_report_snapshots
          WHERE cluster_id = $1
      )
),
prior AS (
    SELECT
        SUM(critical_count)::INTEGER AS critical,
        SUM(high_count)::INTEGER AS high,
        SUM(medium_count)::INTEGER AS medium,
        SUM(low_count)::INTEGER AS low,
        SUM(unknown_count)::INTEGER AS unknown,
        MAX(scanned_at) AS scanned_at
    FROM image_vulnerability_report_snapshots
    WHERE cluster_id = $1
      AND scanned_at <= $2
)
SELECT
    COALESCE(latest.critical, 0)::INTEGER AS latest_critical,
    COALESCE(latest.high, 0)::INTEGER     AS latest_high,
    COALESCE(latest.medium, 0)::INTEGER   AS latest_medium,
    COALESCE(latest.low, 0)::INTEGER      AS latest_low,
    COALESCE(latest.unknown, 0)::INTEGER  AS latest_unknown,
    latest.scanned_at                     AS latest_scanned_at,
    COALESCE(prior.critical, 0)::INTEGER  AS prior_critical,
    COALESCE(prior.high, 0)::INTEGER      AS prior_high,
    COALESCE(prior.medium, 0)::INTEGER    AS prior_medium,
    COALESCE(prior.low, 0)::INTEGER       AS prior_low,
    COALESCE(prior.unknown, 0)::INTEGER   AS prior_unknown,
    prior.scanned_at                      AS prior_scanned_at
FROM latest, prior
`

type CompareImageVulnerabilitySnapshotsForClusterParams struct {
	ClusterID uuid.UUID          `json:"cluster_id"`
	Cutoff    pgtype.Timestamptz `json:"cutoff"`
}

// CompareImageVulnerabilitySnapshotsForClusterRow returns latest-vs-
// prior aggregate counts. The "prior" comparison anchors at the most
// recent snapshot ≤ cutoff so a callsite passing now-24h compares
// today's totals against yesterday's regardless of scan cadence.
type CompareImageVulnerabilitySnapshotsForClusterRow struct {
	LatestCritical  int32              `json:"latest_critical"`
	LatestHigh      int32              `json:"latest_high"`
	LatestMedium    int32              `json:"latest_medium"`
	LatestLow       int32              `json:"latest_low"`
	LatestUnknown   int32              `json:"latest_unknown"`
	LatestScannedAt pgtype.Timestamptz `json:"latest_scanned_at"`
	PriorCritical   int32              `json:"prior_critical"`
	PriorHigh       int32              `json:"prior_high"`
	PriorMedium     int32              `json:"prior_medium"`
	PriorLow        int32              `json:"prior_low"`
	PriorUnknown    int32              `json:"prior_unknown"`
	PriorScannedAt  pgtype.Timestamptz `json:"prior_scanned_at"`
}

func (q *Queries) CompareImageVulnerabilitySnapshotsForCluster(ctx context.Context, arg CompareImageVulnerabilitySnapshotsForClusterParams) (CompareImageVulnerabilitySnapshotsForClusterRow, error) {
	row := q.db.QueryRow(ctx, compareImageVulnerabilitySnapshotsForCluster, arg.ClusterID, arg.Cutoff)
	var i CompareImageVulnerabilitySnapshotsForClusterRow
	err := row.Scan(
		&i.LatestCritical, &i.LatestHigh, &i.LatestMedium, &i.LatestLow, &i.LatestUnknown, &i.LatestScannedAt,
		&i.PriorCritical, &i.PriorHigh, &i.PriorMedium, &i.PriorLow, &i.PriorUnknown, &i.PriorScannedAt,
	)
	return i, err
}

// -- Retention --------------------------------------------------------

const pruneOldImageVulnerabilityReportSnapshots = `-- name: PruneOldImageVulnerabilityReportSnapshots :execrows
DELETE FROM image_vulnerability_report_snapshots
WHERE scanned_at < $1
`

func (q *Queries) PruneOldImageVulnerabilityReportSnapshots(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := q.db.Exec(ctx, pruneOldImageVulnerabilityReportSnapshots, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
