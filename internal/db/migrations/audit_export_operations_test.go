package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestAuditExportOperationsMigrationDefinesDurableArtifactLifecycle(t *testing.T) {
	raw, err := os.ReadFile("045_audit_export_operations.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, required := range []string{
		"CREATE TABLE public.audit_export_operations",
		"request_spec jsonb NOT NULL",
		"artifact bytea",
		"artifact IS NULL OR octet_length(artifact) <= 268435456",
		"audit_export_operations_recovery_idx",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("audit export migration missing %q", required)
		}
	}
}
