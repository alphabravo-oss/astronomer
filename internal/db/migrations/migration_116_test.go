package migrations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration116File(t *testing.T, name string) string {
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

func TestUserIsServiceMigrationContent(t *testing.T) {
	up := loadMigration116File(t, "116_user_is_service.up.sql")
	down := loadMigration116File(t, "116_user_is_service.down.sql")

	// The flag must be added with a DEFAULT so the ADD COLUMN doesn't rewrite
	// the existing users table (check-migrations.sh enforces this too).
	if !strings.Contains(up, "ADD COLUMN IF NOT EXISTS is_service BOOLEAN NOT NULL DEFAULT false") {
		t.Errorf("116 up migration missing the is_service column add")
	}
	// up must be reversible.
	if !strings.Contains(down, "DROP COLUMN IF EXISTS is_service") {
		t.Errorf("116 down migration must drop is_service")
	}
}
