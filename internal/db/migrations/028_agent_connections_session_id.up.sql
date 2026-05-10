ALTER TABLE agent_connections
    ADD COLUMN IF NOT EXISTS session_id VARCHAR(255);

UPDATE agent_connections
SET session_id = 'legacy-' || id::text
WHERE session_id IS NULL OR session_id = '';

ALTER TABLE agent_connections
    ALTER COLUMN session_id SET DEFAULT '';

ALTER TABLE agent_connections
    ALTER COLUMN session_id SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_agent_conns_session_id ON agent_connections (session_id);
