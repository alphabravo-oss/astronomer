CREATE TABLE IF NOT EXISTS cluster_agent_tokens (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id   UUID NOT NULL UNIQUE REFERENCES clusters(id) ON DELETE CASCADE,
    token        VARCHAR(128) NOT NULL UNIQUE,
    last_used_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_cluster_agent_tokens_token ON cluster_agent_tokens (token);

DROP TRIGGER IF EXISTS set_updated_at_cluster_agent_tokens ON cluster_agent_tokens;
CREATE TRIGGER set_updated_at_cluster_agent_tokens
    BEFORE UPDATE ON cluster_agent_tokens
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();
