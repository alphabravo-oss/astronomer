CREATE TABLE public.siem_test_operations (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    forwarder_id uuid NOT NULL REFERENCES public.siem_forwarders(id) ON DELETE CASCADE,
    queue_id bigint UNIQUE,
    idempotency_scope text NOT NULL,
    idempotency_key text NOT NULL,
    request_digest char(64) NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    error_code text NOT NULL DEFAULT '',
    requested_by uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT siem_test_operations_status_valid CHECK (status IN ('pending', 'succeeded', 'failed')),
    CONSTRAINT siem_test_operations_identity_nonempty CHECK (
        length(btrim(idempotency_scope)) BETWEEN 1 AND 512
        AND length(btrim(idempotency_key)) BETWEEN 1 AND 128
        AND length(request_digest) = 64
    ),
    UNIQUE (idempotency_scope, idempotency_key)
);

CREATE INDEX siem_test_operations_forwarder_idx
    ON public.siem_test_operations (forwarder_id, created_at DESC);
