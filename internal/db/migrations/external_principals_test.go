package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestExternalPrincipalsMigrationHasStableIdentityAndSafeOwnership(t *testing.T) {
	contents, err := os.ReadFile("039_external_principals.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(contents)
	for _, contract := range []string{
		"UNIQUE (connector_id, subject)",
		"REFERENCES public.dex_connectors(id) ON DELETE RESTRICT",
		"REFERENCES public.users(id) ON DELETE CASCADE",
		"linked_at timestamptz",
		"email = lower(btrim(email))",
	} {
		if !strings.Contains(sql, contract) {
			t.Errorf("migration missing %q", contract)
		}
	}
}
