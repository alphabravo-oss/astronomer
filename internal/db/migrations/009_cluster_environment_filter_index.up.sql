CREATE INDEX CONCURRENTLY IF NOT EXISTS clusters_active_environment_created_idx
    ON public.clusters (environment, created_at DESC, id DESC)
    WHERE decommissioned_at IS NULL;
