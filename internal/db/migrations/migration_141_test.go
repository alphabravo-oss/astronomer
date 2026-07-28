package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestMigration141RoleCatalogProxyVerb(t *testing.T) {
	up, err := os.ReadFile("141_role_catalog_proxy_verb.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Add-if-missing, builtin-only. A rewrite of the whole rules array, or an
	// update that forgets is_builtin, would clobber operator-authored roles.
	for _, required := range []string{
		"is_builtin = true",
		`'["proxy"]'::jsonb`,
		`'[{"resource":"pods","verbs":["proxy"]}]'::jsonb`,
		`'[{"resource":"services","verbs":["proxy"]}]'::jsonb`,
	} {
		if !strings.Contains(string(up), required) {
			t.Fatalf("up migration missing %q", required)
		}
	}
	// The trap this migration exists to avoid: granting nodes:proxy to a
	// shipped template re-opens the kubelet /run/ RCE that the new proxy gate
	// closes, because nodes/{name}/proxy would then be reachable by a
	// non-admin role again.
	if strings.Contains(string(up), `"resource":"nodes"`) || strings.Contains(string(up), "'nodes'") {
		t.Fatal("up migration must not grant proxy on the nodes resource")
	}
	if strings.Contains(string(up), "Node Operator") {
		t.Fatal("up migration must not widen the Node Operator template")
	}
}
