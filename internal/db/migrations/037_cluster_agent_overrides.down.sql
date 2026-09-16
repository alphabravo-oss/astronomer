ALTER TABLE public.clusters
    DROP CONSTRAINT IF EXISTS clusters_agent_overrides_object,
    DROP COLUMN IF EXISTS agent_overrides;
