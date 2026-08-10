package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestMigration156ArtifactCredentialStateStoresNoCredentialBytes(t *testing.T) {
	up, err := os.ReadFile("156_charlie_artifact_credential_rotation.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(up)
	for _, required := range []string{"pending_request_id", "pending_generation", "materialization_digest", "pending_state", "acknowledged_at"} {
		if !strings.Contains(text, required) {
			t.Fatalf("migration 156 is missing %q", required)
		}
	}
	for _, prohibited := range []string{"credential_value", "credential_bytes", "artifact_credential BYTEA", "artifact_credential TEXT", "secret_value"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(prohibited)) {
			t.Fatalf("migration 156 must not store credential material %q", prohibited)
		}
	}
	down, err := os.ReadFile("156_charlie_artifact_credential_rotation.down.sql")
	if err != nil || !strings.Contains(string(down), "DROP TABLE IF EXISTS charlie_artifact_credential_state") {
		t.Fatal("migration 156 down must drop artifact credential state")
	}
}
