package migrations_test

// Static content test for migration 061_project_catalogs.
//
// The structural invariants the handler + worker depend on:
//
//   - helm_repositories gains a nullable owner_project_id column with
//     ON DELETE CASCADE (project delete drops its owned catalogs).
//   - project_catalog_subscriptions has UNIQUE (project_id, catalog_id)
//     for the no-duplicate-subscription invariant the handler relies on.
//   - The down file drops the subscriptions table BEFORE the
//     owner_project_id column (otherwise the FK to helm_repositories
//     would dangle).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration061File(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	path := filepath.Join(dir, name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestMigration_ProjectCatalogs_UpContent(t *testing.T) {
	up := loadMigration061File(t, "061_project_catalogs.up.sql")

	for _, want := range []string{
		"ALTER TABLE helm_repositories ADD COLUMN IF NOT EXISTS owner_project_id UUID",
		"REFERENCES projects(id) ON DELETE CASCADE",
		"CREATE INDEX IF NOT EXISTS idx_helm_repositories_owner_project",
		"WHERE owner_project_id IS NOT NULL",
		"CREATE TABLE project_catalog_subscriptions",
		"project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE",
		"catalog_id      UUID NOT NULL REFERENCES helm_repositories(id) ON DELETE CASCADE",
		"created_by      UUID REFERENCES users(id) ON DELETE SET NULL",
		"UNIQUE (project_id, catalog_id)",
		"CREATE INDEX idx_project_catalog_subs_project",
		"CREATE INDEX idx_project_catalog_subs_catalog",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing required text %q", want)
		}
	}
}

func TestMigration_ProjectCatalogs_DownContent(t *testing.T) {
	down := loadMigration061File(t, "061_project_catalogs.down.sql")
	for _, want := range []string{
		"DROP TABLE IF EXISTS project_catalog_subscriptions",
		"ALTER TABLE helm_repositories DROP COLUMN IF EXISTS owner_project_id",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("down migration missing required text %q", want)
		}
	}
	// Subscriptions table must drop before the column it references
	// (catalog_id → helm_repositories(id)). Down does the inverse of up.
	posSubs := strings.Index(down, "DROP TABLE IF EXISTS project_catalog_subscriptions")
	posCol := strings.Index(down, "ALTER TABLE helm_repositories DROP COLUMN IF EXISTS owner_project_id")
	if posSubs < 0 || posCol < 0 {
		t.Fatalf("missing expected statements")
	}
	if posSubs > posCol {
		t.Errorf("subscriptions table dropped AFTER owner_project_id column; FK rollback ordering broken")
	}
}
