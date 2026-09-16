ALTER TABLE public.clusters
    ADD COLUMN agent_overrides jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD CONSTRAINT clusters_agent_overrides_object CHECK (
        jsonb_typeof(agent_overrides) = 'object'
        AND pg_column_size(agent_overrides) <= 32768
    );
