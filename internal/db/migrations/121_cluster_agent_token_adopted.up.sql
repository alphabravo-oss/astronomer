-- Task A3 (M2/L2): single-use-by-adoption for registration tokens + unique hash index.
ALTER TABLE cluster_agent_tokens
    ADD COLUMN IF NOT EXISTS adopted_at TIMESTAMPTZ;

-- Backfill: existing live durables predate this column. Stamp them adopted at
-- their last activity so M2 is closed immediately at deploy for already-joined
-- clusters (a leaked, still-unexpired ORIGINAL registration token, created at
-- join time, is < this value and is therefore denied on replay). Re-import
-- still works: a freshly minted token's created_at is > this value.
--
-- last_used_at > created_at is the "agent actually reconnected with the durable"
-- (= adopted) signal: UpsertClusterAgentToken stamps last_used_at = created_at at
-- MINT, and only a later durable-path CONNECT (TouchClusterAgentToken) or a
-- rotation advances it. Gating on it means a cluster that was MID-JOIN at deploy
-- (durable minted but not yet adopted, so the agent is still legitimately
-- presenting the registration token) is NOT stamped — its join-window reg-token
-- reconnects keep working and it is not locked out; it gets adopted_at the normal
-- way on the agent's first durable CONNECT.
UPDATE cluster_agent_tokens
SET adopted_at = COALESCE(last_used_at, last_rotated_at, created_at)
WHERE adopted_at IS NULL AND revoked_at IS NULL AND token_hash <> ''
  AND last_used_at > created_at;

-- L2: promote migration 093's NON-unique partial hash index to PARTIAL UNIQUE.
-- Partial (WHERE token_hash <> '') is MANDATORY: legacy plaintext rows carry
-- token_hash='' (migration 102/093) and would collide under a full unique index.
-- Random 256-bit tokens make a non-empty-hash duplicate cryptographically
-- impossible; if a corrupt env has one, this CREATE fails loud (correct).
DROP INDEX IF EXISTS idx_cluster_registration_tokens_token_hash;
CREATE UNIQUE INDEX IF NOT EXISTS idx_cluster_registration_tokens_token_hash
    ON cluster_registration_tokens (token_hash)
    WHERE token_hash <> '';
