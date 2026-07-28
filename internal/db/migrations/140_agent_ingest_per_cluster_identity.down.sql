-- Restore the pre-140 role definition and reactivate the legacy shared
-- principal, because rolling the code back means IssueAgentIngestToken resolves
-- that bare username again and its tokens must authenticate (the auth
-- middleware requires is_active).
--
-- The shared principal's revoked tokens and deleted bindings are NOT recreated:
-- agents re-mint on CONNECT, and re-granting fleet-wide clusters:update to a
-- shared identity is exactly what 140 removed.
UPDATE cluster_roles
   SET rules = '[{"resource":"clusters","verbs":["update"]}]'::jsonb,
       description = 'Reserved role granting clusters:update for per-cluster apiserver-audit ingest tokens.',
       updated_at = now()
 WHERE name = 'system:agent-ingest';

UPDATE users SET is_active = true, updated_at = now()
 WHERE username = 'system:agent-ingest';
