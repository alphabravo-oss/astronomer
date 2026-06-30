-- Force-delete: when true, the decommission reconciler does NOT wait out the
-- managed-side cleanup grace window (cleanupGraceTimeout, 15m) for a
-- disconnected agent to reconnect — it attempts cleanup once and, if the agent
-- is unreachable, advances straight to token-revoke + tombstone. Used when the
-- operator knows the target cluster/agent is gone and wants immediate removal.
-- Default false preserves the safe grace behavior for normal deletes.
ALTER TABLE cluster_decommissions ADD COLUMN force BOOLEAN NOT NULL DEFAULT false;
