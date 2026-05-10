CREATE TABLE audit_logs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID REFERENCES users(id) ON DELETE SET NULL,
    action        VARCHAR(64) NOT NULL,
    resource_type VARCHAR(64) NOT NULL,
    resource_id   VARCHAR(255) NOT NULL DEFAULT '',
    resource_name VARCHAR(255) NOT NULL DEFAULT '',
    detail        JSONB NOT NULL DEFAULT '{}',
    ip_address    INET,
    user_agent    TEXT NOT NULL DEFAULT '',
    request_id    VARCHAR(64) NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_logs_action_created ON audit_logs (action, created_at);
CREATE INDEX idx_audit_logs_resource ON audit_logs (resource_type, resource_id);
CREATE INDEX idx_audit_logs_user_created ON audit_logs (user_id, created_at);
CREATE INDEX idx_audit_logs_request_id ON audit_logs (request_id);
CREATE INDEX idx_audit_logs_created_at ON audit_logs (created_at);
