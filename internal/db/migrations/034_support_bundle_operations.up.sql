CREATE TABLE public.support_bundle_operations (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    requested_by uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    idempotency_scope text NOT NULL,
    idempotency_key text NOT NULL,
    request_digest char(64) NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    attempt_count integer NOT NULL DEFAULT 0,
    locked_until timestamptz,
    error_code text NOT NULL DEFAULT '',
    filename text NOT NULL DEFAULT '',
    artifact_content_type text NOT NULL DEFAULT 'application/zip',
    artifact bytea,
    artifact_sha256 char(64),
    artifact_size bigint NOT NULL DEFAULT 0,
    expires_at timestamptz NOT NULL DEFAULT (now() + interval '24 hours'),
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT support_bundle_operations_status_valid
        CHECK (status IN ('pending', 'running', 'retrying', 'failed', 'succeeded')),
    CONSTRAINT support_bundle_operations_attempt_count_valid CHECK (attempt_count >= 0),
    CONSTRAINT support_bundle_operations_identity_nonempty CHECK (
        length(btrim(idempotency_scope)) BETWEEN 1 AND 512
        AND length(btrim(idempotency_key)) BETWEEN 1 AND 128
        AND length(request_digest) = 64
    ),
    CONSTRAINT support_bundle_operations_artifact_bounded CHECK (
        artifact IS NULL OR octet_length(artifact) <= 67108864
    ),
    CONSTRAINT support_bundle_operations_artifact_consistent CHECK (
        (status = 'succeeded' AND artifact IS NOT NULL AND artifact_size = octet_length(artifact)
            AND length(artifact_sha256) = 64 AND length(filename) > 0)
        OR (status <> 'succeeded' AND artifact IS NULL AND artifact_size = 0
            AND artifact_sha256 IS NULL)
    ),
    UNIQUE (idempotency_scope, idempotency_key)
);

CREATE INDEX support_bundle_operations_recovery_idx
    ON public.support_bundle_operations (status, locked_until, updated_at)
    WHERE status IN ('pending', 'running', 'retrying');

CREATE INDEX support_bundle_operations_expiry_idx
    ON public.support_bundle_operations (expires_at);

UPDATE public.read_audit_policies
SET path_pattern = '/support-bundles/*/download', updated_at = now()
WHERE name = 'support_bundle_download';
