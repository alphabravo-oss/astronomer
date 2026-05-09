CREATE TABLE monitoring_operations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    target_type VARCHAR(64) NOT NULL,
    target_key VARCHAR(255) NOT NULL,
    operation_type VARCHAR(32) NOT NULL,
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

CREATE INDEX idx_monitoring_operations_status_created
    ON monitoring_operations (status, created_at);

CREATE INDEX idx_monitoring_operations_target
    ON monitoring_operations (target_type, target_key, created_at DESC);
