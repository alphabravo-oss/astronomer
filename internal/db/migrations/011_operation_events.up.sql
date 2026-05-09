CREATE TABLE argocd_operation_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operation_id UUID NOT NULL REFERENCES argocd_operations(id) ON DELETE CASCADE,
    level VARCHAR(16) NOT NULL,
    stage VARCHAR(64) NOT NULL,
    message TEXT NOT NULL,
    detail JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_argocd_operation_events_operation
    ON argocd_operation_events (operation_id, created_at);

CREATE TABLE tool_operation_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operation_id UUID NOT NULL REFERENCES tool_operations(id) ON DELETE CASCADE,
    level VARCHAR(16) NOT NULL,
    stage VARCHAR(64) NOT NULL,
    message TEXT NOT NULL,
    detail JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tool_operation_events_operation
    ON tool_operation_events (operation_id, created_at);

CREATE TABLE catalog_operation_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operation_id UUID NOT NULL REFERENCES catalog_operations(id) ON DELETE CASCADE,
    level VARCHAR(16) NOT NULL,
    stage VARCHAR(64) NOT NULL,
    message TEXT NOT NULL,
    detail JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_catalog_operation_events_operation
    ON catalog_operation_events (operation_id, created_at);
