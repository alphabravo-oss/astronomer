-- Migration 111 — cross-cluster ("fleet-wide") anomaly baselines.
-- Aggregates the per-cluster anomaly_baselines means across clusters
-- and records which clusters are outliers vs. the fleet.

-- name: UpsertXClusterAnomalyBaseline :one
INSERT INTO xcluster_anomaly_baselines (
    metric_name, window_seconds, cluster_count, population_mean, population_stddev,
    population_min, population_max, stddev_mult, outlier_cluster_ids, updated_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, now()
)
ON CONFLICT (metric_name, window_seconds) DO UPDATE SET
    cluster_count       = EXCLUDED.cluster_count,
    population_mean          = EXCLUDED.population_mean,
    population_stddev        = EXCLUDED.population_stddev,
    population_min           = EXCLUDED.population_min,
    population_max           = EXCLUDED.population_max,
    stddev_mult         = EXCLUDED.stddev_mult,
    outlier_cluster_ids = EXCLUDED.outlier_cluster_ids,
    updated_at          = now()
RETURNING id, metric_name, window_seconds, cluster_count, population_mean, population_stddev,
          population_min, population_max, stddev_mult, outlier_cluster_ids, updated_at;
