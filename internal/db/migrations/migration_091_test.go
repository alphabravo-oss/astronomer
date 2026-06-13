package migrations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMig091(t *testing.T, name string) string {
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

func TestFleetOwnershipMetadata_UpContent(t *testing.T) {
	up := loadMig091(t, "091_fleet_ownership_metadata.up.sql")
	mustContain := []string{
		"ADD COLUMN IF NOT EXISTS managed_by",
		"ADD COLUMN IF NOT EXISTS external_ref_api_version",
		"ADD COLUMN IF NOT EXISTS observed_generation",
		"clusters_managed_by_valid",
		"projects_managed_by_valid",
		"clusters_external_ref_all_or_none",
		"projects_external_ref_all_or_none",
		"clusters_external_ref_unique",
		"projects_external_ref_unique",
	}
	for _, s := range mustContain {
		if !strings.Contains(up, s) {
			t.Errorf("up file missing required content:\n  %q", s)
		}
	}
}

func TestFleetOwnershipMetadata_DownContent(t *testing.T) {
	down := loadMig091(t, "091_fleet_ownership_metadata.down.sql")
	for _, s := range []string{
		"DROP INDEX IF EXISTS projects_external_ref_unique",
		"DROP INDEX IF EXISTS clusters_external_ref_unique",
		"DROP COLUMN IF EXISTS observed_generation",
		"DROP COLUMN IF EXISTS managed_by",
	} {
		if !strings.Contains(down, s) {
			t.Errorf("down file missing required content:\n  %q", s)
		}
	}
}
