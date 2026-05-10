DROP INDEX IF EXISTS idx_agent_conns_session_id;

ALTER TABLE agent_connections
    DROP COLUMN IF EXISTS session_id;
