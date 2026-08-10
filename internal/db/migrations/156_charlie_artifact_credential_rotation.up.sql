CREATE TABLE charlie_artifact_credential_state (
    connection_id           UUID PRIMARY KEY REFERENCES charlie_connections(id) ON DELETE CASCADE,
    current_lease_id        VARCHAR(128) NOT NULL DEFAULT '',
    current_generation      BIGINT NOT NULL DEFAULT 0,
    renew_after             TIMESTAMPTZ,
    expires_at              TIMESTAMPTZ,
    pending_request_id      UUID,
    pending_lease_id        VARCHAR(128) NOT NULL DEFAULT '',
    pending_generation      BIGINT NOT NULL DEFAULT 0,
    pending_state           VARCHAR(24) NOT NULL DEFAULT 'idle',
    materialization_digest  VARCHAR(71) NOT NULL DEFAULT '',
    last_error_code         VARCHAR(64) NOT NULL DEFAULT '',
    attempt_count           INTEGER NOT NULL DEFAULT 0,
    acknowledged_at         TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT charlie_artifact_generation_nonnegative CHECK (current_generation >= 0 AND pending_generation >= 0),
    CONSTRAINT charlie_artifact_pending_state CHECK (pending_state IN ('idle', 'claiming', 'claimed', 'materialized')),
    CONSTRAINT charlie_artifact_pending_binding CHECK (
        (pending_state = 'idle' AND pending_request_id IS NULL AND pending_lease_id = '' AND pending_generation = 0 AND materialization_digest = '') OR
        (pending_state = 'claiming' AND pending_request_id IS NOT NULL AND pending_lease_id = '' AND pending_generation = 0 AND materialization_digest = '') OR
        (pending_state = 'claimed' AND pending_request_id IS NOT NULL AND pending_lease_id <> '' AND pending_generation > current_generation AND materialization_digest = '') OR
        (pending_state = 'materialized' AND pending_request_id IS NOT NULL AND pending_lease_id <> '' AND pending_generation > current_generation AND materialization_digest ~ '^sha256:[a-f0-9]{64}$')
    ),
    CONSTRAINT charlie_artifact_expiry_order CHECK (renew_after IS NULL OR expires_at IS NULL OR renew_after < expires_at)
);

CREATE INDEX charlie_artifact_credential_due_idx
    ON charlie_artifact_credential_state (renew_after)
    WHERE pending_state = 'idle';
