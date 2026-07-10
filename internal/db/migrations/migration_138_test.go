package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestMigration138ApplicationDiscoveryIdentity(t *testing.T) {
	up, err := os.ReadFile("138_argocd_application_discovery_identity.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("138_argocd_application_discovery_identity.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"upstream_uid VARCHAR(128) NOT NULL DEFAULT ''",
		"application_namespace VARCHAR(255) NOT NULL DEFAULT ''",
		"never stores Application spec or credentials",
	} {
		if !strings.Contains(string(up), required) {
			t.Fatalf("up migration missing %q", required)
		}
	}
	for _, required := range []string{"DROP COLUMN IF EXISTS application_namespace", "DROP COLUMN IF EXISTS upstream_uid"} {
		if !strings.Contains(string(down), required) {
			t.Fatalf("down migration missing %q", required)
		}
	}
}
