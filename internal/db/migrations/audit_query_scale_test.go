package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestAuditQueryScaleMigration(t *testing.T) {
	up, err := os.ReadFile("030_audit_query_scale.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"CREATE EXTENSION IF NOT EXISTS pg_trgm",
		"idx_audit_log_created_id",
		"created_at DESC, id DESC",
		"idx_audit_log_search_document_trgm",
		"public.gin_trgm_ops",
		"idx_audit_log_detail_path",
		"jsonb_path_ops",
		"idx_kubectl_sessions_active_started_at",
		"WHERE status IN ('starting', 'active')",
	} {
		if !strings.Contains(string(up), required) {
			t.Fatalf("audit query scale migration missing %q", required)
		}
	}

	down, err := os.ReadFile("030_audit_query_scale.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"idx_kubectl_sessions_active_started_at",
		"idx_audit_log_detail_path",
		"idx_audit_log_source_document_trgm",
		"idx_audit_log_search_document_trgm",
		"idx_audit_log_created_id",
	} {
		if !strings.Contains(string(down), "DROP INDEX IF EXISTS public."+name) {
			t.Fatalf("audit query scale rollback missing %q", name)
		}
	}
}
