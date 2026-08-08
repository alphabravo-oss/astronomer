package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestMigration155InteractiveThreadsHasNoContentColumns(t *testing.T) {
	up, err := os.ReadFile("155_charlie_interactive_threads.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(up)
	for _, required := range []string{
		"CREATE TABLE charlie_interactive_threads",
		"CREATE TABLE charlie_thread_sessions",
		"charlie_interactive_threads_one_active",
		"state IN ('active', 'archived')",
		"ADD COLUMN IF NOT EXISTS thread_id",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("migration 155 is missing %q", required)
		}
	}
	for _, prohibited := range []string{
		"prompt", "response", "message_body", "tool_result", "encrypted_content",
		"model_output", "evidence_blob",
	} {
		if strings.Contains(strings.ToLower(text), prohibited) {
			t.Fatalf("migration 155 must not store content field %q", prohibited)
		}
	}
	down, err := os.ReadFile("155_charlie_interactive_threads.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS charlie_interactive_threads") {
		t.Fatal("migration 155 down must drop interactive threads")
	}
}
