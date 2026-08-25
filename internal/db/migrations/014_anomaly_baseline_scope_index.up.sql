CREATE INDEX CONCURRENTLY IF NOT EXISTS anomaly_baselines_cluster_updated_id_idx
    ON public.anomaly_baselines (cluster_id, updated_at DESC, id DESC);
