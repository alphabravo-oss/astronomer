package migrations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration104File(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func TestArgoCDApplicationResourceDriftCountsMigrationContent(t *testing.T) {
	up := loadMigration104File(t, "104_argocd_application_resource_drift_counts.up.sql")
	down := loadMigration104File(t, "104_argocd_application_resource_drift_counts.down.sql")

	for _, want := range []string{
		"ADD COLUMN IF NOT EXISTS resource_created_count INTEGER NOT NULL DEFAULT 0",
		"ADD COLUMN IF NOT EXISTS resource_changed_count INTEGER NOT NULL DEFAULT 0",
		"ADD COLUMN IF NOT EXISTS resource_pruned_count INTEGER NOT NULL DEFAULT 0",
		"argocd_application_resource_counts_nonnegative",
		"resource_created_count >= 0",
		"resource_changed_count >= 0",
		"resource_pruned_count >= 0",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("104 up migration missing required content %q", want)
		}
	}

	for _, want := range []string{
		"DROP CONSTRAINT IF EXISTS argocd_application_resource_counts_nonnegative",
		"DROP COLUMN IF EXISTS resource_pruned_count",
		"DROP COLUMN IF EXISTS resource_changed_count",
		"DROP COLUMN IF EXISTS resource_created_count",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("104 down migration missing required content %q", want)
		}
	}
}
