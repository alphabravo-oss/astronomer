package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestSupportBundleOperationsAreDurableAndBounded(t *testing.T) {
	raw, err := os.ReadFile("034_support_bundle_operations.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, required := range []string{
		"CREATE TABLE public.support_bundle_operations",
		"UNIQUE (idempotency_scope, idempotency_key)",
		"octet_length(artifact) <= 67108864",
		"CREATE INDEX support_bundle_operations_recovery_idx",
		"CREATE INDEX support_bundle_operations_expiry_idx",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("support bundle migration missing %q", required)
		}
	}
	if strings.Contains(strings.ToLower(sql), "create index concurrently") {
		t.Fatal("transactional migration must not use CREATE INDEX CONCURRENTLY")
	}
}
