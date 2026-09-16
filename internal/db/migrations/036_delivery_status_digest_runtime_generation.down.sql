DROP TRIGGER IF EXISTS bump_delivery_rollout_runtime_from_labels ON public.clusters;
DROP FUNCTION IF EXISTS public.bump_delivery_rollout_runtime_for_labels();
DROP TRIGGER IF EXISTS bump_delivery_rollout_runtime_from_connection ON public.agent_connections;
DROP FUNCTION IF EXISTS public.bump_delivery_rollout_runtime_for_connection();
DROP TRIGGER IF EXISTS bump_delivery_rollout_runtime_from_deployment ON public.cluster_deployments;
DROP FUNCTION IF EXISTS public.bump_delivery_rollout_runtime_for_deployment();
DROP TRIGGER IF EXISTS bump_delivery_rollout_runtime_from_cluster ON public.delivery_rollout_clusters;
DROP FUNCTION IF EXISTS public.bump_delivery_rollout_runtime_for_cluster_row();

ALTER TABLE public.delivery_rollouts
    DROP CONSTRAINT IF EXISTS delivery_rollout_runtime_generation_valid,
    DROP COLUMN IF EXISTS runtime_generation;

ALTER TABLE public.delivery_controller_inventory
    DROP CONSTRAINT IF EXISTS delivery_controller_status_sequence_valid,
    DROP CONSTRAINT IF EXISTS delivery_controller_status_digest_valid,
    DROP COLUMN IF EXISTS semantic_sequence,
    DROP COLUMN IF EXISTS agent_sequence,
    DROP COLUMN IF EXISTS agent_session_id,
    DROP COLUMN IF EXISTS status_digest;
