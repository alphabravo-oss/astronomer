-- Supports latest-session lookups used by the connection metrics and Charlie
-- fleet projections. The existing (cluster_id, status) index cannot satisfy
-- ORDER BY connected_at DESC efficiently.
CREATE INDEX IF NOT EXISTS idx_agent_connections_cluster_connected_at
    ON public.agent_connections (cluster_id, connected_at DESC);

-- Makes the daily history prune seek directly into terminal rows instead of
-- scanning live sessions and the full connection archive.
CREATE INDEX IF NOT EXISTS idx_agent_connections_terminal_disconnected_at
    ON public.agent_connections (disconnected_at)
    WHERE status <> 'connected';
