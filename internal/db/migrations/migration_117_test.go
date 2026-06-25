package migrations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration117File(t *testing.T, name string) string {
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

func TestUIExtensionBundleVerifiedMigrationContent(t *testing.T) {
	up := loadMigration117File(t, "117_ui_extension_bundle_verified.up.sql")
	down := loadMigration117File(t, "117_ui_extension_bundle_verified.down.sql")

	// The flag must be added with a DEFAULT so the ADD COLUMN doesn't rewrite
	// the existing ui_extensions table (check-migrations.sh enforces this too)
	// and so the gate fails closed: nothing is bundle-verified by default.
	if !strings.Contains(up, "ADD COLUMN IF NOT EXISTS bundle_verified BOOLEAN NOT NULL DEFAULT false") {
		t.Errorf("117 up migration missing the bundle_verified column add")
	}
	// up must be reversible.
	if !strings.Contains(down, "DROP COLUMN IF EXISTS bundle_verified") {
		t.Errorf("117 down migration must drop bundle_verified")
	}
}
