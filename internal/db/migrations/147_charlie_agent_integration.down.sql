DROP TABLE IF EXISTS charlie_finding_resources;
DROP TABLE IF EXISTS charlie_findings;
DROP TABLE IF EXISTS charlie_trigger_events;
DROP TABLE IF EXISTS charlie_trigger_rules;
DROP TABLE IF EXISTS charlie_action_deferrals;
DROP TABLE IF EXISTS charlie_automation_policies;
DROP TABLE IF EXISTS charlie_action_receipts;
DROP TABLE IF EXISTS charlie_action_approvals;
DROP TRIGGER IF EXISTS revoke_charlie_delegations_project_role_bindings ON project_role_bindings;
DROP TRIGGER IF EXISTS revoke_charlie_delegations_project_roles ON project_roles;
DROP TRIGGER IF EXISTS revoke_charlie_delegations_cluster_role_bindings ON cluster_role_bindings;
DROP TRIGGER IF EXISTS revoke_charlie_delegations_cluster_roles ON cluster_roles;
DROP TRIGGER IF EXISTS revoke_charlie_delegations_global_role_bindings ON global_role_bindings;
DROP TRIGGER IF EXISTS revoke_charlie_delegations_global_roles ON global_roles;
DROP TRIGGER IF EXISTS revoke_charlie_delegations_connection_inactive ON charlie_connections;
DROP TRIGGER IF EXISTS revoke_charlie_delegations_user_deactivated ON users;
DROP FUNCTION IF EXISTS revoke_charlie_delegations_on_rbac_change();
DROP FUNCTION IF EXISTS revoke_charlie_delegations_for_inactive_connection();
DROP FUNCTION IF EXISTS revoke_charlie_delegations_for_deactivated_user();
DROP TABLE IF EXISTS charlie_delegations;
DROP TABLE IF EXISTS charlie_session_resources;
DROP TABLE IF EXISTS charlie_sessions;
DROP TABLE IF EXISTS charlie_connections;
DROP TABLE IF EXISTS tunnel_locator_events;
DROP TABLE IF EXISTS agent_connection_events;
DROP TABLE IF EXISTS agent_operational_statuses;
DELETE FROM global_roles WHERE name IN ('Charlie Approver', 'Charlie Automation') AND is_builtin = true;
UPDATE global_roles
SET rules = COALESCE((
    SELECT jsonb_agg(rule)
    FROM jsonb_array_elements(rules) AS rule
    WHERE rule ->> 'resource' <> 'charlie'
), '[]'::jsonb),
updated_at = now()
WHERE name IN ('Read Only', 'Platform Operator');
DELETE FROM platform_settings WHERE key = 'feature.charlie';
