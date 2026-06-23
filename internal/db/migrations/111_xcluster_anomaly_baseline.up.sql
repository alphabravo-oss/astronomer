-- P1 item 5/22 — cross-cluster ("fleet-wide") anomaly baselines.
--
-- Per-cluster baselines (migration 072, anomaly_baselines) answer
-- "is THIS cluster deviating from its OWN history?". This table
-- answers the orthogonal question: "is this cluster an OUTLIER vs.
-- the rest of the fleet RIGHT NOW?".
--
-- One row per (metric_name, window_seconds). The recompute worker
-- reads every per-cluster baseline for that (metric, window), treats
-- each cluster's `mean` as a single fleet datapoint, and computes the
-- fleet mean/stddev. Any cluster whose per-cluster mean deviates from
-- the fleet mean by more than `stddev_mult` population stddevs is
-- recorded in `outlier_cluster_ids`.
--
-- `outlier_cluster_ids` is a JSONB array of cluster UUID strings so a
-- dashboard / alert path can read the flagged set in one indexed
-- lookup without re-deriving it. `cluster_count` gates the same
-- cold-start false-positive class as the per-cluster min_samples gate:
-- with < 3 clusters the fleet stddev is meaningless and we flag
-- nothing.

CREATE TABLE xcluster_anomaly_baselines (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    metric_name         VARCHAR(128) NOT NULL,
    window_seconds      INTEGER NOT NULL DEFAULT 86400,
    -- Number of clusters that contributed a baseline this pass.
    cluster_count       INTEGER NOT NULL DEFAULT 0,
    -- Fleet aggregate over the per-cluster means.
    fleet_mean          DOUBLE PRECISION NOT NULL DEFAULT 0,
    fleet_stddev        DOUBLE PRECISION NOT NULL DEFAULT 0,
    fleet_min           DOUBLE PRECISION NOT NULL DEFAULT 0,
    fleet_max           DOUBLE PRECISION NOT NULL DEFAULT 0,
    -- Outlier threshold (population stddevs from fleet_mean).
    stddev_mult         DOUBLE PRECISION NOT NULL DEFAULT 3.0,
    -- JSONB array of cluster UUID strings flagged as outliers.
    outlier_cluster_ids JSONB NOT NULL DEFAULT '[]',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (metric_name, window_seconds)
);
CREATE INDEX idx_xcluster_anomaly_baselines_metric ON xcluster_anomaly_baselines (metric_name);
CREATE INDEX idx_xcluster_anomaly_baselines_updated_at ON xcluster_anomaly_baselines (updated_at);
