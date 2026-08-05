package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestMigration148AlignsOpaqueCharlieIdentity(t *testing.T) {
	upBytes, err := os.ReadFile("148_charlie_opaque_onboarding_ids.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := string(upBytes)
	for _, required := range []string{
		"udt_name = 'uuid'",
		"onboarding_package_id TYPE VARCHAR(128)",
		"ADD COLUMN product_slug VARCHAR(63)",
		"product_slug = 'astronomer'",
	} {
		if !strings.Contains(up, required) {
			t.Errorf("migration 148 missing %q", required)
		}
	}
	if strings.Contains(strings.ToLower(up), "delete from") {
		t.Fatal("identity migration must not delete connection state")
	}
}
