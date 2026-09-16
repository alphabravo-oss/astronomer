package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestAgentConnectionsOneActiveMigration(t *testing.T) {
	up, err := os.ReadFile("029_agent_connections_one_active.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS agent_connections_one_active_per_cluster",
		"ON public.agent_connections (cluster_id)",
		"WHERE status = 'connected'",
	} {
		if !strings.Contains(string(up), required) {
			t.Fatalf("one-active-connection migration missing %q", required)
		}
	}
}
