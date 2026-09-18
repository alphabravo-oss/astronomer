ALTER TABLE public.delivery_targets
    ADD COLUMN overrides jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD CONSTRAINT delivery_target_overrides_valid CHECK (
        jsonb_typeof(overrides) = 'object' AND pg_column_size(overrides) <= 65536
    );

ALTER TABLE public.cluster_deployments
    ADD COLUMN desired_overrides jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN previous_overrides jsonb,
    ADD CONSTRAINT cluster_deployment_desired_overrides_valid CHECK (
        jsonb_typeof(desired_overrides) = 'object' AND pg_column_size(desired_overrides) <= 65536
    ),
    ADD CONSTRAINT cluster_deployment_previous_overrides_valid CHECK (
        previous_overrides IS NULL OR (jsonb_typeof(previous_overrides) = 'object' AND pg_column_size(previous_overrides) <= 65536)
    );
