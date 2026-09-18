ALTER TABLE public.delivery_controller_inventory
    ADD COLUMN status_digest varchar(80) NOT NULL DEFAULT 'sha256:0000000000000000000000000000000000000000000000000000000000000000',
    ADD COLUMN agent_session_id varchar(128) NOT NULL DEFAULT 'migration',
    ADD COLUMN agent_sequence bigint NOT NULL DEFAULT 1,
    ADD COLUMN semantic_sequence bigint NOT NULL DEFAULT 1,
    ADD CONSTRAINT delivery_controller_status_digest_valid
        CHECK (status_digest ~ '^sha256:[0-9a-f]{64}$'),
    ADD CONSTRAINT delivery_controller_status_sequence_valid
        CHECK (agent_sequence > 0 AND semantic_sequence > 0 AND semantic_sequence <= agent_sequence);

ALTER TABLE public.delivery_controller_inventory
    ALTER COLUMN status_digest DROP DEFAULT,
    ALTER COLUMN agent_session_id DROP DEFAULT,
    ALTER COLUMN agent_sequence DROP DEFAULT,
    ALTER COLUMN semantic_sequence DROP DEFAULT;

ALTER TABLE public.delivery_rollouts
    ADD COLUMN runtime_generation bigint NOT NULL DEFAULT 1,
    ADD CONSTRAINT delivery_rollout_runtime_generation_valid CHECK (runtime_generation > 0);

CREATE OR REPLACE FUNCTION public.bump_delivery_rollout_runtime_for_cluster_row()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    UPDATE public.delivery_rollouts
    SET runtime_generation = runtime_generation + 1
    WHERE id = NEW.rollout_id;
    RETURN NEW;
END;
$$;

CREATE TRIGGER bump_delivery_rollout_runtime_from_cluster
AFTER UPDATE OF state, assignment_action, attempt, fence, released_at, acknowledged_at,
    ready_at, completed_at, deadline, last_error_code ON public.delivery_rollout_clusters
FOR EACH ROW EXECUTE FUNCTION public.bump_delivery_rollout_runtime_for_cluster_row();

CREATE OR REPLACE FUNCTION public.bump_delivery_rollout_runtime_for_deployment()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    UPDATE public.delivery_rollouts
    SET runtime_generation = runtime_generation + 1
    WHERE id IN (
        COALESCE(NEW.current_rollout_id, OLD.current_rollout_id),
        COALESCE(OLD.current_rollout_id, NEW.current_rollout_id)
    );
    RETURN COALESCE(NEW, OLD);
END;
$$;

CREATE TRIGGER bump_delivery_rollout_runtime_from_deployment
AFTER INSERT OR UPDATE OR DELETE ON public.cluster_deployments
FOR EACH ROW EXECUTE FUNCTION public.bump_delivery_rollout_runtime_for_deployment();

CREATE OR REPLACE FUNCTION public.bump_delivery_rollout_runtime_for_connection()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    UPDATE public.delivery_rollouts AS rollout
    SET runtime_generation = rollout.runtime_generation + 1
    WHERE rollout.state IN ('resolving', 'awaiting_approval', 'queued', 'progressing', 'rolling_back')
      AND EXISTS (
          SELECT 1
          FROM public.delivery_rollout_clusters AS cluster
          WHERE cluster.rollout_id = rollout.id
            AND cluster.cluster_id IN (
                COALESCE(NEW.cluster_id, OLD.cluster_id),
                COALESCE(OLD.cluster_id, NEW.cluster_id)
            )
      );
    RETURN COALESCE(NEW, OLD);
END;
$$;

CREATE TRIGGER bump_delivery_rollout_runtime_from_connection
AFTER INSERT OR UPDATE OF status, disconnected_at OR DELETE ON public.agent_connections
FOR EACH ROW EXECUTE FUNCTION public.bump_delivery_rollout_runtime_for_connection();

CREATE OR REPLACE FUNCTION public.bump_delivery_rollout_runtime_for_labels()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    UPDATE public.delivery_rollouts AS rollout
    SET runtime_generation = rollout.runtime_generation + 1
    WHERE rollout.state IN ('resolving', 'awaiting_approval', 'queued', 'progressing', 'rolling_back')
      AND EXISTS (
          SELECT 1
          FROM public.delivery_rollout_clusters AS cluster
          WHERE cluster.rollout_id = rollout.id AND cluster.cluster_id = NEW.id
      );
    RETURN NEW;
END;
$$;

CREATE TRIGGER bump_delivery_rollout_runtime_from_labels
AFTER UPDATE OF labels ON public.clusters
FOR EACH ROW
WHEN (OLD.labels IS DISTINCT FROM NEW.labels)
EXECUTE FUNCTION public.bump_delivery_rollout_runtime_for_labels();
