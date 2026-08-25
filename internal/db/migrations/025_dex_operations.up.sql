CREATE TABLE public.dex_operations (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    action text NOT NULL,
    target_id uuid NOT NULL,
    runtime_generation bigint NOT NULL DEFAULT 0,
    idempotency_scope text NOT NULL,
    idempotency_key text NOT NULL,
    request_digest char(64) NOT NULL,
    payload_encrypted text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'pending',
    phase text NOT NULL DEFAULT 'queued',
    attempt_count integer NOT NULL DEFAULT 0,
    locked_until timestamptz,
    error_code text NOT NULL DEFAULT '',
    created_by uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT dex_operations_action_valid CHECK (action IN ('apply', 'register_sso')),
    CONSTRAINT dex_operations_status_valid CHECK (status IN ('pending', 'running', 'retrying', 'failed', 'succeeded')),
    CONSTRAINT dex_operations_phase_valid CHECK (phase IN ('queued', 'reconciling', 'finalizing', 'completed')),
    CONSTRAINT dex_operations_generation_valid CHECK (runtime_generation >= 0),
    CONSTRAINT dex_operations_attempt_valid CHECK (attempt_count >= 0),
    CONSTRAINT dex_operations_identity_nonempty CHECK (
        length(btrim(idempotency_scope)) BETWEEN 1 AND 512
        AND length(btrim(idempotency_key)) BETWEEN 1 AND 128
        AND length(request_digest) = 64
    ),
    UNIQUE (idempotency_scope, idempotency_key)
);

CREATE UNIQUE INDEX dex_operations_active_target_idx
    ON public.dex_operations (target_id)
    WHERE status IN ('pending', 'running', 'retrying');

CREATE INDEX dex_operations_recovery_idx
    ON public.dex_operations (status, locked_until, updated_at)
    WHERE status IN ('pending', 'running', 'retrying');
