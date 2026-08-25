CREATE INDEX CONCURRENTLY IF NOT EXISTS logging_operations_cluster_created_idx
    ON public.logging_operations (((payload->>'cluster_id')::uuid), created_at DESC, id DESC)
    WHERE payload->>'cluster_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';
