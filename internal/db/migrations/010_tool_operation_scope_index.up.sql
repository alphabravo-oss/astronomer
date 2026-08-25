-- Malformed legacy envelopes stay outside this guarded expression index and
-- consequently fail closed for restricted collection reads.
CREATE INDEX CONCURRENTLY IF NOT EXISTS tool_operations_cluster_created_idx
    ON public.tool_operations (((payload->>'clusterId')::uuid), created_at DESC, id DESC)
    WHERE payload->>'clusterId' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';
