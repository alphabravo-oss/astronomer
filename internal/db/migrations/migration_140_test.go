package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestMigration140AgentIngestPerClusterIdentity(t *testing.T) {
	up, err := os.ReadFile("140_agent_ingest_per_cluster_identity.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("140_agent_ingest_per_cluster_identity.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`'[{"resource":"audit_ingest","verbs":["create"]}]'::jsonb`,
		"DELETE FROM cluster_role_bindings",
		"SET is_revoked = true",
		"is_active = false",
	} {
		if !strings.Contains(string(up), required) {
			t.Fatalf("up migration missing %q", required)
		}
	}
	// The trap: the cleanup must key on the BARE legacy username. A LIKE /
	// prefix match would also hit the new per-cluster principals
	// ("system:agent-ingest:<uuid>") and strip the fleet of its ingest grants.
	if strings.Contains(string(up), "system:agent-ingest%") {
		t.Fatal("up migration must not prefix-match the per-cluster principals")
	}
	// Rolling back re-points the old code at the shared principal, so the row
	// must be usable again (the auth middleware requires is_active).
	for _, required := range []string{
		`'[{"resource":"clusters","verbs":["update"]}]'::jsonb`,
		"is_active = true",
	} {
		if !strings.Contains(string(down), required) {
			t.Fatalf("down migration missing %q", required)
		}
	}
}
