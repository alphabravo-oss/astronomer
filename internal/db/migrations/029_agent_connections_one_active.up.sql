CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS agent_connections_one_active_per_cluster
    ON public.agent_connections (cluster_id)
    WHERE status = 'connected';
