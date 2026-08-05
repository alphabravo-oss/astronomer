package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestMigration147CharlieIntegrationSecurityContract(t *testing.T) {
	upBytes, err := os.ReadFile("147_charlie_agent_integration.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := string(upBytes)

	for _, required := range []string{
		"'feature.charlie', 'false'::jsonb",
		"CREATE TABLE agent_operational_statuses",
		"CREATE TABLE agent_connection_events",
		"CREATE TABLE tunnel_locator_events",
		"CREATE TABLE charlie_connections",
		"CREATE TABLE charlie_sessions",
		"CREATE TABLE charlie_session_resources",
		"CREATE TABLE charlie_delegations",
		"CREATE TRIGGER revoke_charlie_delegations_user_deactivated",
		"CREATE TRIGGER revoke_charlie_delegations_connection_inactive",
		"CREATE FUNCTION revoke_charlie_delegations_on_rbac_change()",
		"CREATE TRIGGER revoke_charlie_delegations_global_role_bindings",
		"CREATE TRIGGER revoke_charlie_delegations_cluster_role_bindings",
		"CREATE TRIGGER revoke_charlie_delegations_project_role_bindings",
		"CREATE TABLE charlie_action_approvals",
		"CREATE TABLE charlie_action_receipts",
		"CONSTRAINT charlie_receipt_arguments_present",
		"CONSTRAINT charlie_receipt_terminal_result",
		"CREATE TABLE charlie_automation_policies",
		"CREATE TABLE charlie_action_deferrals",
		"CREATE TABLE charlie_trigger_rules",
		"CREATE TABLE charlie_trigger_events",
		"retry_of_event_id     UUID REFERENCES charlie_trigger_events(id) ON DELETE SET NULL",
		"charlie_trigger_event_active_operator_retry_idx",
		"CREATE TABLE charlie_findings",
		"approval_id                VARCHAR(128) UNIQUE",
		"charlie_finding_approval_mode",
		"CREATE TABLE charlie_finding_resources",
		"local_trust_material_encrypted",
		"onboarding_package_expires_at",
		"enrollment_credentials_expires_at",
		"artifact_credential_expires_at",
		"certificate_expires_at",
		"emergency_disabled",
		"onboarding_state",
		"replica_count                   INTEGER NOT NULL DEFAULT 2",
		"CONSTRAINT charlie_connection_replica_count CHECK (replica_count BETWEEN 2 AND 20)",
		"state IN ('claimed', 'blocked', 'waiting_approval', 'deferred', 'dispatched', 'ambiguous'",
	} {
		if !strings.Contains(up, required) {
			t.Errorf("migration 147 missing %q", required)
		}
	}

	for _, forbidden := range []string{
		"prompt TEXT",
		"response TEXT",
		"raw_evidence",
		"tool_arguments",
		"tool_results",
		"runtime_token",
		"api_key",
	} {
		if strings.Contains(strings.ToLower(up), strings.ToLower(forbidden)) {
			t.Errorf("migration 147 contains forbidden content/credential column %q", forbidden)
		}
	}

	downBytes, err := os.ReadFile("147_charlie_agent_integration.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	down := string(downBytes)
	for _, table := range []string{"charlie_finding_resources", "charlie_findings", "charlie_trigger_events", "charlie_trigger_rules", "charlie_action_deferrals", "charlie_automation_policies", "charlie_action_receipts", "charlie_action_approvals", "charlie_delegations", "charlie_session_resources", "charlie_sessions", "charlie_connections", "tunnel_locator_events", "agent_connection_events", "agent_operational_statuses"} {
		if !strings.Contains(down, "DROP TABLE IF EXISTS "+table) {
			t.Errorf("down migration does not drop %s", table)
		}
	}
	for _, trigger := range []string{"revoke_charlie_delegations_user_deactivated", "revoke_charlie_delegations_connection_inactive", "revoke_charlie_delegations_global_roles", "revoke_charlie_delegations_global_role_bindings", "revoke_charlie_delegations_cluster_roles", "revoke_charlie_delegations_cluster_role_bindings", "revoke_charlie_delegations_project_roles", "revoke_charlie_delegations_project_role_bindings"} {
		if !strings.Contains(down, "DROP TRIGGER IF EXISTS "+trigger) {
			t.Errorf("down migration does not drop %s", trigger)
		}
	}
}
