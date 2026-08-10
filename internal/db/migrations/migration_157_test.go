package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestMigration157FindingCursorIsContentFree(t *testing.T) {
	up, err := os.ReadFile("157_charlie_finding_projection_cursor.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(up))
	for _, required := range []string{"connection_id", "sequence", "last_error_code"} {
		if !strings.Contains(text, required) {
			t.Fatalf("migration 157 is missing %q", required)
		}
	}
	for _, prohibited := range []string{"summary", "evidence", "prompt", "argument", "credential", "content"} {
		if strings.Contains(text, prohibited) {
			t.Fatalf("migration 157 must not store %q", prohibited)
		}
	}
}
