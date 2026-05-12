-- Migration 072 — rolling-window baselines for "deviation from
-- baseline" alert rules. Queries here are mirrored in the
-- hand-authored shim at internal/db/sqlc/anomaly_baselines_ext.sql.go
-- so the build keeps passing on agent worktrees where the sqlc CLI
-- is occasionally not available.

-- name: ListAnomalyBaselines :many
SELECT id, cluster_id, metric_name, window_seconds, sample_count, mean, stddev,
       min_value, max_value, p50, p95, p99, last_value, last_value_at,
       recent_samples, updated_at
FROM anomaly_baselines
ORDER BY updated_at DESC
LIMIT $1 OFFSET $2;

-- name: ListAnomalyBaselinesByCluster :many
SELECT id, cluster_id, metric_name, window_seconds, sample_count, mean, stddev,
       min_value, max_value, p50, p95, p99, last_value, last_value_at,
       recent_samples, updated_at
FROM anomaly_baselines
WHERE cluster_id = $1
ORDER BY metric_name ASC;

-- name: GetAnomalyBaseline :one
SELECT id, cluster_id, metric_name, window_seconds, sample_count, mean, stddev,
       min_value, max_value, p50, p95, p99, last_value, last_value_at,
       recent_samples, updated_at
FROM anomaly_baselines
WHERE cluster_id = $1 AND metric_name = $2 AND window_seconds = $3;

-- name: GetAnomalyBaselineByID :one
SELECT id, cluster_id, metric_name, window_seconds, sample_count, mean, stddev,
       min_value, max_value, p50, p95, p99, last_value, last_value_at,
       recent_samples, updated_at
FROM anomaly_baselines
WHERE id = $1;

-- name: UpsertAnomalyBaseline :one
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
RETURNING id, cluster_id, metric_name, window_seconds, sample_count, mean, stddev,
          min_value, max_value, p50, p95, p99, last_value, last_value_at,
          recent_samples, updated_at;

-- name: DeleteAnomalyBaseline :exec
DELETE FROM anomaly_baselines WHERE id = $1;

-- name: CountAnomalyBaselines :one
SELECT count(*) FROM anomaly_baselines;
