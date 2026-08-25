CREATE INDEX CONCURRENTLY IF NOT EXISTS catalog_operations_cluster_created_idx
    ON public.catalog_operations (((payload->>'clusterId')::uuid), created_at DESC, id DESC)
    WHERE payload->>'clusterId' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';
