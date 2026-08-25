package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestNodeOperationMigrationPinsDurabilityContract(t *testing.T) {
	raw, err := os.ReadFile("023_node_operations.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, required := range []string{
		"UNIQUE (idempotency_scope, idempotency_key)",
		"parameters_encrypted text NOT NULL",
		"generation bigint NOT NULL DEFAULT 1",
		"observed_generation bigint NOT NULL DEFAULT 0",
		"locked_until timestamptz",
		"progress jsonb NOT NULL",
		"node_operations_recovery_idx",
		"UNIQUE INDEX node_operations_active_target_idx",
		"WHERE status IN ('pending', 'running', 'retrying')",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("node operation migration missing %q", required)
		}
	}
}
