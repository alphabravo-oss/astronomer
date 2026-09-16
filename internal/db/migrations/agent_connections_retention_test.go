package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestAgentConnectionRetentionIndexMigration(t *testing.T) {
	up, err := os.ReadFile("027_agent_connections_retention.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"CREATE INDEX IF NOT EXISTS idx_agent_connections_cluster_connected_at",
		"ON public.agent_connections (cluster_id, connected_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_agent_connections_terminal_disconnected_at",
		"ON public.agent_connections (disconnected_at)",
		"WHERE status <> 'connected'",
	} {
		if !strings.Contains(string(up), required) {
			t.Fatalf("agent connection retention migration missing %q", required)
		}
	}

	down, err := os.ReadFile("027_agent_connections_retention.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"DROP INDEX IF EXISTS public.idx_agent_connections_terminal_disconnected_at",
		"DROP INDEX IF EXISTS public.idx_agent_connections_cluster_connected_at",
	} {
		if !strings.Contains(string(down), required) {
			t.Fatalf("agent connection retention rollback missing %q", required)
		}
	}
}
