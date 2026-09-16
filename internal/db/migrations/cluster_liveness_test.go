package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestClusterLivenessMigrationKeepsHeartbeatWritesNarrow(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("040_cluster_liveness.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"CREATE TABLE public.cluster_liveness",
		"cluster_id uuid PRIMARY KEY",
		"commands_pending boolean NOT NULL DEFAULT false",
		"CREATE INDEX idx_agent_lifecycle_operations_actionable",
		"WHERE status IN ('pending', 'running')",
		"CREATE TRIGGER refresh_cluster_commands_pending",
		"DROP INDEX public.idx_clusters_heartbeat",
		"ALTER TABLE public.clusters DROP COLUMN last_heartbeat",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration missing %q", want)
		}
	}
}
