CREATE TABLE public.node_operations (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    idempotency_scope text NOT NULL,
    idempotency_key text NOT NULL,
    request_digest char(64) NOT NULL,
    cluster_id uuid NOT NULL,
    node_name text NOT NULL,
    action text NOT NULL,
    parameters_encrypted text NOT NULL,
    generation bigint NOT NULL DEFAULT 1,
    observed_generation bigint NOT NULL DEFAULT 0,
    status text NOT NULL DEFAULT 'pending',
    attempt_count integer NOT NULL DEFAULT 0,
    locked_until timestamptz,
    error_code text NOT NULL DEFAULT '',
    progress jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_by_id uuid,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT node_operations_action_valid CHECK (action IN (
        'cordon', 'uncordon', 'set_label', 'remove_label',
        'set_annotation', 'remove_annotation', 'add_taint',
        'remove_taint', 'drain'
    )),
    CONSTRAINT node_operations_status_valid CHECK (status IN (
        'pending', 'running', 'retrying', 'blocked', 'failed', 'succeeded'
    )),
    CONSTRAINT node_operations_generation_valid CHECK (
        generation > 0 AND observed_generation >= 0 AND observed_generation <= generation
    ),
    CONSTRAINT node_operations_attempt_valid CHECK (attempt_count >= 0),
    CONSTRAINT node_operations_identity_nonempty CHECK (
        length(trim(idempotency_scope)) > 0
        AND length(trim(idempotency_key)) > 0
        AND length(trim(node_name)) > 0
        AND length(parameters_encrypted) > 0
    ),
    CONSTRAINT node_operations_progress_object CHECK (jsonb_typeof(progress) = 'object'),
    CONSTRAINT node_operations_created_by_fkey FOREIGN KEY (created_by_id)
        REFERENCES public.users(id) ON DELETE SET NULL,
    UNIQUE (idempotency_scope, idempotency_key)
);

CREATE INDEX node_operations_recovery_idx
    ON public.node_operations (status, locked_until, updated_at)
    WHERE status IN ('pending', 'running', 'retrying');

CREATE INDEX node_operations_target_idx
    ON public.node_operations (cluster_id, node_name, created_at DESC);

-- A node is an ordered external-effect target. Prevent separately keyed
-- cordon/uncordon/drain intents from racing and applying out of order.
CREATE UNIQUE INDEX node_operations_active_target_idx
    ON public.node_operations (cluster_id, node_name)
    WHERE status IN ('pending', 'running', 'retrying');
