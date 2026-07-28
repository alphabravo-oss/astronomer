-- Per-cluster apiserver-audit ingest identity (P0-1).
--
-- Every cluster's ingest token used to be owned by ONE service user
-- ("system:agent-ingest") that accumulated a cluster_role_binding per connected
-- cluster, and the shared reserved role granted clusters:update — the same verb
-- that opens exec tickets and k8s-proxy writes. Any single agent's ingest token
-- therefore carried fleet-wide cluster-mutation authority.
--
-- The code now derives a per-cluster principal ("system:agent-ingest:<uuid>")
-- and the reserved role grants only audit_ingest:create. Both of the rows below
-- must be reconciled here because the runtime get-or-create paths deliberately
-- never rewrite a row that already exists.

-- 1. Narrow the shared role. Without this, upgraded installs keep granting
--    clusters:update AND fail the newly narrowed ingest gate.
UPDATE cluster_roles
   SET rules = '[{"resource":"audit_ingest","verbs":["create"]}]'::jsonb,
       description = 'Reserved role granting audit_ingest:create for per-cluster apiserver-audit ingest tokens.',
       updated_at = now()
 WHERE name = 'system:agent-ingest';

-- 2. Retire the legacy shared principal. Its fleet-wide bindings and live
--    tokens ARE the vulnerability, so leaving them would mean an upgraded
--    install is not actually fixed. Matching the bare username leaves the new
--    per-cluster rows (which carry a ":<uuid>" suffix) untouched. A connected
--    agent still holds the plaintext of the token revoked here, so its next
--    AUDIT_DELIVERY=http batch gets a 401; httpAuditSender treats a 401/403 as
--    "this token is dead", degrades to the tunnel, and picks the HTTP path back
--    up when the next CONNECT mints a fresh scoped token. No audit events are
--    lost and no operator action (rolling restart) is required.
UPDATE api_tokens t
   SET is_revoked = true
  FROM users u
 WHERE t.user_id = u.id
   AND u.username = 'system:agent-ingest'
   AND t.is_revoked = false;

DELETE FROM cluster_role_bindings
 WHERE user_id IN (SELECT id FROM users WHERE username = 'system:agent-ingest');

UPDATE users SET is_active = false, updated_at = now()
 WHERE username = 'system:agent-ingest';
