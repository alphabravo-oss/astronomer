package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestResourceOperationMigrationHasDurabilitySecrecyAndFences(t *testing.T) {
	up, err := os.ReadFile("022_resource_operations.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(up))
	for _, required := range []string{
		"create table public.resource_operations",
		"manifest_encrypted text not null",
		"unique (idempotency_scope, idempotency_key)",
		"generation bigint not null",
		"observed_generation bigint not null",
		"locked_until timestamptz",
		"resource_operations_recovery_idx",
		"resource_operations_active_target_unique",
		"on public.resource_operations (cluster_id, resource_type, namespace, resource_name)",
		"action in ('apply', 'delete')",
		"required_verb in ('create', 'update', 'delete')",
		"status in ('pending', 'running', 'retrying', 'failed', 'succeeded')",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration 022 missing %q", required)
		}
	}
	for _, forbidden := range []string{"manifest json", "manifest jsonb", "secret_data", "plaintext"} {
		if strings.Contains(sql, forbidden) {
			t.Errorf("migration 022 contains unsafe plaintext column marker %q", forbidden)
		}
	}

	down, err := os.ReadFile("022_resource_operations.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(string(down)), "drop table if exists public.resource_operations") {
		t.Fatal("migration 022 is not reversible")
	}
}
