CREATE INDEX CONCURRENTLY IF NOT EXISTS clusters_active_provider_created_idx
    ON public.clusters (provider, created_at DESC, id DESC)
    WHERE decommissioned_at IS NULL;
