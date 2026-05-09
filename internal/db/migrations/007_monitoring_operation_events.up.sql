CREATE TABLE monitoring_operation_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operation_id UUID NOT NULL REFERENCES monitoring_operations(id) ON DELETE CASCADE,
    level VARCHAR(16) NOT NULL DEFAULT 'info',
    stage VARCHAR(64) NOT NULL,
    message TEXT NOT NULL,
    detail JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_monitoring_operation_events_operation_created
    ON monitoring_operation_events (operation_id, created_at ASC);
