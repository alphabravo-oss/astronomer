package migrations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration122File(t *testing.T, name string) string {
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

// TestGitOpsMassDecommissionOverrideMigrationContent asserts task E3's migration
// adds the one-shot allow_mass_decommission override flag with a safe default
// (false) declared on the same line as NOT NULL (required by check-migrations.sh),
// and that the down cleanly drops it.
func TestGitOpsMassDecommissionOverrideMigrationContent(t *testing.T) {
	up := loadMigration122File(t, "122_gitops_mass_decommission_override.up.sql")
	down := loadMigration122File(t, "122_gitops_mass_decommission_override.down.sql")

	if !strings.Contains(up, "ADD COLUMN allow_mass_decommission BOOLEAN NOT NULL DEFAULT false") {
		t.Error("122 up must ADD COLUMN allow_mass_decommission BOOLEAN NOT NULL DEFAULT false (default safe + on the same line as NOT NULL)")
	}
	if !strings.Contains(down, "DROP COLUMN allow_mass_decommission") {
		t.Error("122 down must DROP COLUMN allow_mass_decommission")
	}
}
