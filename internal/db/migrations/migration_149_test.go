package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestMigration149PersistsSignedCharlieArtifactReferences(t *testing.T) {
	upBytes, err := os.ReadFile("149_charlie_artifact_references.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := string(upBytes)
	for _, required := range []string{
		"ADD COLUMN chart_reference VARCHAR(512)",
		"ADD COLUMN image_reference VARCHAR(512)",
		"ALTER COLUMN chart_reference SET NOT NULL",
		"ALTER COLUMN image_reference SET NOT NULL",
		"charlie_connection_chart_reference_oci",
		"charlie_connection_image_reference_pinned",
		"@sha256:[0-9a-f]{64}$",
	} {
		if !strings.Contains(up, required) {
			t.Errorf("migration 149 missing %q", required)
		}
	}
	if strings.Contains(strings.ToLower(up), "delete from") {
		t.Fatal("artifact reference migration must preserve existing connection history")
	}

	downBytes, err := os.ReadFile("149_charlie_artifact_references.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	down := string(downBytes)
	for _, required := range []string{"DROP COLUMN IF EXISTS image_reference", "DROP COLUMN IF EXISTS chart_reference"} {
		if !strings.Contains(down, required) {
			t.Errorf("migration 149 down missing %q", required)
		}
	}
}
