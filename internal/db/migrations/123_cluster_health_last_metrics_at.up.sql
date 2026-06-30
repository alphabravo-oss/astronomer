-- C3 / M13: track the last time a NON-EMPTY metrics sample actually arrived,
-- separate from last_check (which all three writers — heartbeat, metrics, and
-- the worker health sweep — refresh). This lets the periodic sweep tell a
-- frozen-but-present metrics stream ("MetricsStale") apart from a cluster that
-- has no metrics-server at all ("NoMetricsServer"). Nullable + DEFAULT NULL: a
-- NULL value means "no metrics sample has ever been received".
ALTER TABLE cluster_health_statuses ADD COLUMN last_metrics_at TIMESTAMPTZ DEFAULT NULL;
