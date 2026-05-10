DROP TRIGGER IF EXISTS set_updated_at_cluster_agent_tokens ON cluster_agent_tokens;
DROP INDEX IF EXISTS idx_cluster_agent_tokens_token;
DROP TABLE IF EXISTS cluster_agent_tokens;
