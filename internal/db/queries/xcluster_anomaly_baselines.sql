-- Migration 111 — cross-cluster ("fleet-wide") anomaly baselines.
-- Aggregates the per-cluster anomaly_baselines means across clusters
-- and records which clusters are outliers vs. the fleet.

-- name: ListXClusterAnomalyBaselines :many
SELECT id, metric_name, window_seconds, cluster_count, fleet_mean, fleet_stddev,
       fleet_min, fleet_max, stddev_mult, outlier_cluster_ids, updated_at
FROM xcluster_anomaly_baselines
ORDER BY metric_name ASC;

-- name: GetXClusterAnomalyBaseline :one
SELECT id, metric_name, window_seconds, cluster_count, fleet_mean, fleet_stddev,
       fleet_min, fleet_max, stddev_mult, outlier_cluster_ids, updated_at
FROM xcluster_anomaly_baselines
WHERE metric_name = $1 AND window_seconds = $2;

-- name: UpsertXClusterAnomalyBaseline :one
INSERT INTO xcluster_anomaly_baselines (
    metric_name, window_seconds, cluster_count, fleet_mean, fleet_stddev,
    fleet_min, fleet_max, stddev_mult, outlier_cluster_ids, updated_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, now()
)
ON CONFLICT (metric_name, window_seconds) DO UPDATE SET
    cluster_count       = EXCLUDED.cluster_count,
    fleet_mean          = EXCLUDED.fleet_mean,
    fleet_stddev        = EXCLUDED.fleet_stddev,
    fleet_min           = EXCLUDED.fleet_min,
    fleet_max           = EXCLUDED.fleet_max,
    stddev_mult         = EXCLUDED.stddev_mult,
    outlier_cluster_ids = EXCLUDED.outlier_cluster_ids,
    updated_at          = now()
RETURNING id, metric_name, window_seconds, cluster_count, fleet_mean, fleet_stddev,
          fleet_min, fleet_max, stddev_mult, outlier_cluster_ids, updated_at;
