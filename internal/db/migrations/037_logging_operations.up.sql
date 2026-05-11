-- Logging operations: durable, controller-reconciled queue for applying
-- logging outputs + pipelines into managed clusters. Mirrors the shape of
-- catalog_operations / tool_operations so the handler can enqueue rows and
-- a background reconciler walks pending work, talks to the agent via the
-- tunnel K8sRequester, and emits per-stage events.
CREATE TABLE logging_operations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    target_type VARCHAR(32) NOT NULL,          -- 'output' or 'pipeline'
    target_key VARCHAR(255) NOT NULL,          -- the output/pipeline UUID as text
    operation_type VARCHAR(32) NOT NULL,       -- 'apply' | 'delete'
    payload JSONB NOT NULL DEFAULT '{}',
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error_message TEXT NOT NULL DEFAULT '',
    created_by_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_logging_operations_status_created
    ON logging_operations (status, created_at);

CREATE INDEX idx_logging_operations_target
    ON logging_operations (target_type, target_key, created_at DESC);

CREATE TABLE logging_operation_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operation_id UUID NOT NULL REFERENCES logging_operations(id) ON DELETE CASCADE,
    level VARCHAR(16) NOT NULL,
    stage VARCHAR(64) NOT NULL,
    message TEXT NOT NULL,
    detail JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_logging_operation_events_operation
    ON logging_operation_events (operation_id, created_at);
