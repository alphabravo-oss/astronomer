CREATE TABLE public.audit_export_operations (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    requested_by uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    request_digest char(64) NOT NULL,
    request_spec jsonb NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    attempt_count integer NOT NULL DEFAULT 0,
    locked_until timestamptz,
    error_code text NOT NULL DEFAULT '',
    filename text NOT NULL DEFAULT '',
    artifact_content_type text NOT NULL DEFAULT 'text/csv; charset=utf-8',
    artifact bytea,
    artifact_sha256 char(64),
    artifact_size bigint NOT NULL DEFAULT 0,
    expires_at timestamptz NOT NULL DEFAULT (now() + interval '24 hours'),
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT audit_export_operations_status_valid
        CHECK (status IN ('pending', 'running', 'retrying', 'failed', 'succeeded')),
    CONSTRAINT audit_export_operations_request_valid CHECK (
        length(request_digest) = 64 AND jsonb_typeof(request_spec) = 'object'
    ),
    CONSTRAINT audit_export_operations_artifact_bounded CHECK (
        artifact IS NULL OR octet_length(artifact) <= 268435456
    ),
    CONSTRAINT audit_export_operations_artifact_consistent CHECK (
        (status = 'succeeded' AND artifact IS NOT NULL AND artifact_size = octet_length(artifact)
            AND length(artifact_sha256) = 64 AND length(filename) > 0)
        OR (status <> 'succeeded' AND artifact IS NULL AND artifact_size = 0
            AND artifact_sha256 IS NULL)
    ),
    UNIQUE (requested_by, request_digest)
);

CREATE INDEX audit_export_operations_recovery_idx
    ON public.audit_export_operations (status, locked_until, updated_at)
    WHERE status IN ('pending', 'running', 'retrying');

CREATE INDEX audit_export_operations_expiry_idx
    ON public.audit_export_operations (expires_at);
