CREATE INDEX CONCURRENTLY IF NOT EXISTS clusters_active_status_created_idx
    ON public.clusters (status, created_at DESC, id DESC)
    WHERE decommissioned_at IS NULL;
