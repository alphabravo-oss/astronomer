ALTER TABLE public.clusters ADD COLUMN last_heartbeat timestamptz;
UPDATE public.clusters c
SET last_heartbeat = l.last_heartbeat
FROM public.cluster_liveness l
WHERE l.cluster_id = c.id;
CREATE INDEX idx_clusters_heartbeat ON public.clusters (last_heartbeat);

DROP TRIGGER IF EXISTS refresh_cluster_commands_pending ON public.agent_lifecycle_operations;
DROP FUNCTION IF EXISTS public.refresh_cluster_commands_pending();
DROP INDEX IF EXISTS public.idx_agent_lifecycle_operations_actionable;
DROP TABLE IF EXISTS public.cluster_liveness;
