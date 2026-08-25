CREATE INDEX CONCURRENTLY IF NOT EXISTS installed_charts_cluster_created_id_idx
    ON public.installed_charts (cluster_id, created_at DESC, id DESC);
