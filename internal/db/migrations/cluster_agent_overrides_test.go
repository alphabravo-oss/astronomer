package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestClusterAgentOverridesMigrationIsBoundedJSONB(t *testing.T) {
	data, err := os.ReadFile("037_cluster_agent_overrides.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	for _, required := range []string{"agent_overrides jsonb NOT NULL DEFAULT '{}'::jsonb", "jsonb_typeof(agent_overrides) = 'object'", "pg_column_size(agent_overrides) <= 32768"} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration missing %q", required)
		}
	}
}
