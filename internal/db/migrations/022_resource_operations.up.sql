CREATE TABLE public.resource_operations (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    idempotency_scope text NOT NULL,
    idempotency_key text NOT NULL,
    request_digest char(64) NOT NULL,
    cluster_id uuid NOT NULL,
    resource_type text NOT NULL,
    namespace text NOT NULL DEFAULT '',
    resource_name text NOT NULL,
    action text NOT NULL,
    required_verb text NOT NULL,
    api_path text NOT NULL,
    manifest_encrypted text NOT NULL DEFAULT '',
    force_apply boolean NOT NULL DEFAULT false,
    generation bigint NOT NULL DEFAULT 1,
    observed_generation bigint NOT NULL DEFAULT 0,
    status text NOT NULL DEFAULT 'pending',
    attempt_count integer NOT NULL DEFAULT 0,
    locked_until timestamptz,
    error_code text NOT NULL DEFAULT '',
    observed_status_code integer,
    observed_resource_version text NOT NULL DEFAULT '',
    created_by_id uuid,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT resource_operations_action_valid CHECK (action IN ('apply', 'delete')),
    CONSTRAINT resource_operations_required_verb_valid CHECK (required_verb IN ('create', 'update', 'delete')),
    CONSTRAINT resource_operations_status_valid CHECK (status IN ('pending', 'running', 'retrying', 'failed', 'succeeded')),
    CONSTRAINT resource_operations_generation_valid CHECK (generation > 0 AND observed_generation >= 0 AND observed_generation <= generation),
    CONSTRAINT resource_operations_attempt_valid CHECK (attempt_count >= 0),
    CONSTRAINT resource_operations_identity_nonempty CHECK (
        length(trim(idempotency_scope)) > 0 AND length(trim(idempotency_key)) > 0
        AND length(trim(resource_type)) > 0 AND length(trim(resource_name)) > 0
        AND length(trim(api_path)) > 0
    ),
    CONSTRAINT resource_operations_apply_manifest_required CHECK (action <> 'apply' OR length(manifest_encrypted) > 0),
    CONSTRAINT resource_operations_created_by_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL,
    UNIQUE (idempotency_scope, idempotency_key)
);

CREATE INDEX resource_operations_recovery_idx
    ON public.resource_operations (status, locked_until, updated_at)
    WHERE status IN ('pending', 'running', 'retrying');

CREATE INDEX resource_operations_target_idx
    ON public.resource_operations (cluster_id, resource_type, namespace, resource_name, created_at DESC);

-- Serialize all active intents for one Kubernetes object. Per-row generation
-- fences retries of the same operation; this target fence prevents a create,
-- update, or delete in a different row from overtaking it.
CREATE UNIQUE INDEX resource_operations_active_target_unique
    ON public.resource_operations (cluster_id, resource_type, namespace, resource_name)
    WHERE status IN ('pending', 'running', 'retrying');
