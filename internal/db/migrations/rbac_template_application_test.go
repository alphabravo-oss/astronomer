package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestRBACTemplateApplicationAndUserSearchMigration(t *testing.T) {
	up, err := os.ReadFile("032_rbac_template_application_and_user_search.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"source_template varchar(128)",
		"source_digest char(64)",
		"uq_project_roles_template_digest",
		"idx_users_directory_search_trgm",
		"public.gin_trgm_ops",
		"coalesce(username, '')",
		"WHERE is_service = false",
	} {
		if !strings.Contains(string(up), required) {
			t.Fatalf("RBAC template migration missing %q", required)
		}
	}

	down, err := os.ReadFile("032_rbac_template_application_and_user_search.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"DROP INDEX IF EXISTS public.idx_users_directory_search_trgm",
		"DROP INDEX IF EXISTS public.uq_project_roles_template_digest",
		"DROP COLUMN IF EXISTS source_digest",
		"DROP COLUMN IF EXISTS source_template",
	} {
		if !strings.Contains(string(down), required) {
			t.Fatalf("RBAC template rollback missing %q", required)
		}
	}
}
