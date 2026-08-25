package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestDexOperationMigrationPinsSagaContract(t *testing.T) {
	raw, err := os.ReadFile("025_dex_operations.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, required := range []string{
		"UNIQUE (idempotency_scope, idempotency_key)",
		"payload_encrypted text NOT NULL",
		"runtime_generation bigint NOT NULL",
		"locked_until timestamptz",
		"completed_at timestamptz",
		"dex_operations_active_target_idx",
		"dex_operations_recovery_idx",
		"status IN ('pending', 'running', 'retrying', 'failed', 'succeeded')",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("Dex operation migration missing %q", required)
		}
	}
}
