package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration135DexGenerationAndIdentityContract(t *testing.T) {
	up, err := os.ReadFile("135_dex_runtime_generation.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(up))
	for _, required := range []string{"chart_release_name", "deployment_name", "service_name", "runtime_generation", "runtime_applied_generation", "create trigger dex_connectors_runtime_generation", "after insert or update or delete"} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration missing %q", required)
		}
	}
	for _, forbidden := range []string{"digest(", "sha", "md5", "clientsecret", "bindpw"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("generation must remain opaque and non-content-derived: %q", forbidden)
		}
	}
	query, err := os.ReadFile("../queries/auth.sql")
	if err != nil {
		t.Fatal(err)
	}
	q := strings.ToLower(string(query))
	for _, required := range []string{"enabledexssoforgeneration", "runtime_applied_generation = sqlc.arg(runtime_generation)"} {
		if !strings.Contains(q, required) {
			t.Fatalf("conditional activation query missing %q", required)
		}
	}
}
