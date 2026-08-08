package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestMigration152KeepsCharlieAlertAuthorityProductLocal(t *testing.T) {
	data, err := os.ReadFile("152_charlie_alert_policy.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := string(data)
	for _, required := range []string{
		"CREATE TABLE charlie_alert_policies", "minimum_severity", "dedupe_window_seconds",
		"escalation_after_seconds", "quiet_hours_timezone", "charlie_alert_policy_channels",
		"notification_channel_id", "CREATE TABLE charlie_alert_deliveries", "last_error_code",
		"charlie_alert_delivery_deep_link", "status IN ('queued', 'retry')",
	} {
		if !strings.Contains(up, required) {
			t.Errorf("migration 152 missing %q", required)
		}
	}
	for _, forbidden := range []string{"approval_id", "authorization", "api_key", "secret_encrypted"} {
		if strings.Contains(strings.ToLower(up), forbidden) {
			t.Errorf("alert policy must not carry authority or credentials: %q", forbidden)
		}
	}
	down, err := os.ReadFile("152_charlie_alert_policy.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS charlie_alert_policies") {
		t.Fatal("migration 152 down is incomplete")
	}
}

func TestCharlieAlertQueriesPinOptimisticUpdatesAndDurableRecovery(t *testing.T) {
	data, err := os.ReadFile("../queries/charlie.sql")
	if err != nil {
		t.Fatal(err)
	}
	queries := string(data)
	for _, required := range []string{
		"WHERE sqlc.arg(expected_revision)::bigint = 0",
		"OR EXISTS (SELECT 1 FROM charlie_alert_policies WHERE connection_id = $1)",
		"WHERE charlie_alert_policies.revision = sqlc.arg(expected_revision)::bigint",
		"status = 'delivering' AND updated_at <= now() - interval '2 minutes'",
		"-- name: CharlieAlertDeliveryAllowed :one",
		"JOIN charlie_connections c ON c.id = d.connection_id",
		"c.active = true AND c.emergency_disabled = false",
		"c.requested_mode <> 'disabled' AND c.verified_mode <> 'disabled'",
		"CASE d.severity WHEN 'critical' THEN 5",
		"NOT EXISTS (\n              SELECT 1 FROM charlie_alert_deliveries d",
		"JOIN LATERAL (\n    SELECT resource_type, resource_id FROM charlie_finding_resources",
	} {
		if !strings.Contains(queries, required) {
			t.Errorf("Charlie alert queries missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"LEFT JOIN LATERAL (\n    SELECT resource_type, resource_id FROM charlie_finding_resources",
		"COALESCE(scope.resource_type",
		"COALESCE(scope.resource_id",
	} {
		if strings.Contains(queries, forbidden) {
			t.Errorf("Charlie alert reconciliation admits an unscoped partial finding: %q", forbidden)
		}
	}
}
