-- Task A2: durable agent-token rotation grace + standalone revocation.
--
--   previous_token_hash  — the hash of the token being rotated out. During
--                          the grace window a CONNECT presenting EITHER the
--                          current token_hash OR this previous_token_hash
--                          validates, so an agent that has not yet adopted
--                          the freshly-minted token is never locked out.
--   rotation_pending_at  — admin / fleet-op / periodic trigger. When set, the
--                          NEXT CONNECT mints a fresh token, delivers it in the
--                          CONNECT_ACK, and clears this flag. Setting it does
--                          NOT change the live token.
--   last_rotated_at      — bookkeeping + the window the periodic policy uses to
--                          decide which clusters are due for rotation.
--
-- revoked_at (migration 103) stays as-is; this task finally wires it.
ALTER TABLE cluster_agent_tokens
    ADD COLUMN IF NOT EXISTS previous_token_hash TEXT,
    ADD COLUMN IF NOT EXISTS rotation_pending_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_rotated_at TIMESTAMPTZ;

-- The grace-window lookup matches previous_token_hash; index it so the
-- OR-branch of GetClusterAgentTokenByToken stays cheap.
CREATE INDEX IF NOT EXISTS idx_cluster_agent_tokens_previous_token_hash
    ON cluster_agent_tokens (previous_token_hash)
    WHERE previous_token_hash IS NOT NULL;

-- The periodic rotation policy scans on last_rotated_at; index it.
CREATE INDEX IF NOT EXISTS idx_cluster_agent_tokens_last_rotated_at
    ON cluster_agent_tokens (last_rotated_at);
