package migrations_test

import (
	"strings"
	"testing"
)

func TestMigration130ProjectNamespacesUniqueClusterNamespace(t *testing.T) {
	up := loadMigrationFile(t, "130_project_namespaces_unique_cluster_namespace.up.sql")
	for _, needle := range []string{
		"CREATE UNIQUE INDEX IF NOT EXISTS uq_project_namespaces_cluster_namespace",
		"ON project_namespaces (cluster_id, namespace)",
		// The redundant non-unique index on the same columns is dropped.
		"DROP INDEX IF EXISTS idx_project_namespaces_cluster",
	} {
		if !strings.Contains(up, needle) {
			t.Fatalf("migration 130 up missing %q", needle)
		}
	}
}

func TestMigration130DownRestoresNonUniqueIndex(t *testing.T) {
	down := loadMigrationFile(t, "130_project_namespaces_unique_cluster_namespace.down.sql")
	for _, needle := range []string{
		"DROP INDEX IF EXISTS uq_project_namespaces_cluster_namespace",
		"CREATE INDEX IF NOT EXISTS idx_project_namespaces_cluster",
	} {
		if !strings.Contains(down, needle) {
			t.Fatalf("migration 130 down missing %q", needle)
		}
	}
}
