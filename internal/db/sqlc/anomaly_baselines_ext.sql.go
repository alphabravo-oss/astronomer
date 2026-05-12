// Migration 072 — anomaly-detection rolling baselines.
//
// Hand-authored sqlc shim. See cloud_credentials.sql.go for the
// rationale (sqlc CLI not runnable in agent worktrees). The contents
// below mirror what the sqlc generator would emit for the queries in
// internal/db/queries/anomaly_baselines.sql.

package sqlc

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// AnomalyBaseline is the row shape for anomaly_baselines. RecentSamples
// is a JSONB array of floats — the recompute worker writes the bounded
// ring buffer, the evaluator reads the aggregate columns directly.
type AnomalyBaseline struct {
	ID             uuid.UUID          `json:"id"`
	ClusterID      uuid.UUID          `json:"cluster_id"`
	MetricName     string             `json:"metric_name"`
	WindowSeconds  int32              `json:"window_seconds"`
	SampleCount    int32              `json:"sample_count"`
	Mean           float64            `json:"mean"`
	Stddev         float64            `json:"stddev"`
	MinValue       float64            `json:"min_value"`
	MaxValue       float64            `json:"max_value"`
	P50            float64            `json:"p50"`
	P95            float64            `json:"p95"`
	P99            float64            `json:"p99"`
	LastValue      float64            `json:"last_value"`
	LastValueAt    pgtype.Timestamptz `json:"last_value_at"`
	RecentSamples  json.RawMessage    `json:"recent_samples"`
	UpdatedAt      time.Time          `json:"updated_at"`
}

const anomalyBaselineColumns = `id, cluster_id, metric_name, window_seconds, sample_count, mean, stddev, min_value, max_value, p50, p95, p99, last_value, last_value_at, recent_samples, updated_at`

func scanAnomalyBaselineRow(row interface {
	Scan(dest ...any) error
}) (AnomalyBaseline, error) {
	var b AnomalyBaseline
	err := row.Scan(
		&b.ID,
		&b.ClusterID,
		&b.MetricName,
		&b.WindowSeconds,
		&b.SampleCount,
		&b.Mean,
		&b.Stddev,
		&b.MinValue,
		&b.MaxValue,
		&b.P50,
		&b.P95,
		&b.P99,
		&b.LastValue,
		&b.LastValueAt,
		&b.RecentSamples,
		&b.UpdatedAt,
	)
	return b, err
}

const listAnomalyBaselines = `-- name: ListAnomalyBaselines :many
SELECT ` + anomalyBaselineColumns + `
FROM anomaly_baselines
ORDER BY updated_at DESC
LIMIT $1 OFFSET $2`

type ListAnomalyBaselinesParams struct {
	Limit  int32 `json:"limit"`
	Offset int32 `json:"offset"`
}

func (q *Queries) ListAnomalyBaselines(ctx context.Context, arg ListAnomalyBaselinesParams) ([]AnomalyBaseline, error) {
	rows, err := q.db.Query(ctx, listAnomalyBaselines, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AnomalyBaseline{}
	for rows.Next() {
		i, err := scanAnomalyBaselineRow(rows)
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

const listAnomalyBaselinesByCluster = `-- name: ListAnomalyBaselinesByCluster :many
SELECT ` + anomalyBaselineColumns + `
FROM anomaly_baselines
WHERE cluster_id = $1
ORDER BY metric_name ASC`

func (q *Queries) ListAnomalyBaselinesByCluster(ctx context.Context, clusterID uuid.UUID) ([]AnomalyBaseline, error) {
	rows, err := q.db.Query(ctx, listAnomalyBaselinesByCluster, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AnomalyBaseline{}
	for rows.Next() {
		i, err := scanAnomalyBaselineRow(rows)
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

const getAnomalyBaseline = `-- name: GetAnomalyBaseline :one
SELECT ` + anomalyBaselineColumns + `
FROM anomaly_baselines
WHERE cluster_id = $1 AND metric_name = $2 AND window_seconds = $3`

type GetAnomalyBaselineParams struct {
	ClusterID     uuid.UUID `json:"cluster_id"`
	MetricName    string    `json:"metric_name"`
	WindowSeconds int32     `json:"window_seconds"`
}

func (q *Queries) GetAnomalyBaseline(ctx context.Context, arg GetAnomalyBaselineParams) (AnomalyBaseline, error) {
	row := q.db.QueryRow(ctx, getAnomalyBaseline, arg.ClusterID, arg.MetricName, arg.WindowSeconds)
	return scanAnomalyBaselineRow(row)
}

const getAnomalyBaselineByID = `-- name: GetAnomalyBaselineByID :one
SELECT ` + anomalyBaselineColumns + `
FROM anomaly_baselines
WHERE id = $1`

func (q *Queries) GetAnomalyBaselineByID(ctx context.Context, id uuid.UUID) (AnomalyBaseline, error) {
	row := q.db.QueryRow(ctx, getAnomalyBaselineByID, id)
	return scanAnomalyBaselineRow(row)
}

const upsertAnomalyBaseline = `-- name: UpsertAnomalyBaseline :one
INSERT INTO anomaly_baselines (
    cluster_id, metric_name, window_seconds, sample_count, mean, stddev,
    min_value, max_value, p50, p95, p99, last_value, last_value_at,
    recent_samples, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11, $12, $13,
    $14, now()
)
ON CONFLICT (cluster_id, metric_name, window_seconds) DO UPDATE SET
    sample_count   = EXCLUDED.sample_count,
    mean           = EXCLUDED.mean,
    stddev         = EXCLUDED.stddev,
    min_value      = EXCLUDED.min_value,
    max_value      = EXCLUDED.max_value,
    p50            = EXCLUDED.p50,
    p95            = EXCLUDED.p95,
    p99            = EXCLUDED.p99,
    last_value     = EXCLUDED.last_value,
    last_value_at  = EXCLUDED.last_value_at,
    recent_samples = EXCLUDED.recent_samples,
    updated_at     = now()
RETURNING ` + anomalyBaselineColumns

type UpsertAnomalyBaselineParams struct {
	ClusterID     uuid.UUID          `json:"cluster_id"`
	MetricName    string             `json:"metric_name"`
	WindowSeconds int32              `json:"window_seconds"`
	SampleCount   int32              `json:"sample_count"`
	Mean          float64            `json:"mean"`
	Stddev        float64            `json:"stddev"`
	MinValue      float64            `json:"min_value"`
	MaxValue      float64            `json:"max_value"`
	P50           float64            `json:"p50"`
	P95           float64            `json:"p95"`
	P99           float64            `json:"p99"`
	LastValue     float64            `json:"last_value"`
	LastValueAt   pgtype.Timestamptz `json:"last_value_at"`
	RecentSamples json.RawMessage    `json:"recent_samples"`
}

func (q *Queries) UpsertAnomalyBaseline(ctx context.Context, arg UpsertAnomalyBaselineParams) (AnomalyBaseline, error) {
	row := q.db.QueryRow(ctx, upsertAnomalyBaseline,
		arg.ClusterID,
		arg.MetricName,
		arg.WindowSeconds,
		arg.SampleCount,
		arg.Mean,
		arg.Stddev,
		arg.MinValue,
		arg.MaxValue,
		arg.P50,
		arg.P95,
		arg.P99,
		arg.LastValue,
		arg.LastValueAt,
		arg.RecentSamples,
	)
	return scanAnomalyBaselineRow(row)
}

const deleteAnomalyBaseline = `-- name: DeleteAnomalyBaseline :exec
DELETE FROM anomaly_baselines WHERE id = $1`

func (q *Queries) DeleteAnomalyBaseline(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, deleteAnomalyBaseline, id)
	return err
}

const countAnomalyBaselines = `-- name: CountAnomalyBaselines :one
SELECT count(*) FROM anomaly_baselines`

func (q *Queries) CountAnomalyBaselines(ctx context.Context) (int64, error) {
	row := q.db.QueryRow(ctx, countAnomalyBaselines)
	var n int64
	err := row.Scan(&n)
	return n, err
}
