ALTER TABLE public.cluster_deployments
    DROP CONSTRAINT IF EXISTS cluster_deployment_previous_overrides_valid,
    DROP CONSTRAINT IF EXISTS cluster_deployment_desired_overrides_valid,
    DROP COLUMN IF EXISTS previous_overrides,
    DROP COLUMN IF EXISTS desired_overrides;

ALTER TABLE public.delivery_targets
    DROP CONSTRAINT IF EXISTS delivery_target_overrides_valid,
    DROP COLUMN IF EXISTS overrides;
