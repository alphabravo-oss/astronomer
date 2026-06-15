package migrations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration102File(t *testing.T, name string) string {
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

func TestClusterRegistrationTokenHashOnly_DropsPlaintextUniqueConstraint(t *testing.T) {
	up := loadMigration102File(t, "102_cluster_registration_token_hash_only.up.sql")

	for _, want := range []string{
		"DROP CONSTRAINT IF EXISTS cluster_registration_tokens_token_key",
		"DROP INDEX IF EXISTS cluster_registration_tokens_token_key",
		"CREATE INDEX IF NOT EXISTS idx_cluster_registration_tokens_token_nonempty",
		"WHERE token <> ''",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("102 up migration missing required content %q", want)
		}
	}
}
