package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestMigration142RoleCatalogPodStreamVerbs(t *testing.T) {
	up, err := os.ReadFile("142_role_catalog_pod_stream_verbs.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Add-if-missing, builtin-only. A rewrite of the whole rules array, or an
	// update that forgets is_builtin, would clobber operator-authored roles.
	for _, required := range []string{
		"is_builtin = true",
		`'["logs"]'::jsonb`,
		`'[{"resource":"pods","verbs":["logs"]}]'::jsonb`,
		"name = 'Cluster Member'",
		"name = 'Cluster Viewer'",
		// Project roles DO reach the WS consumers: expandProjectBindings
		// rewrites a project binding into namespace-scoped cluster bindings
		// when namespace_scoped_rbac_enabled is on, so 'Project Viewer'
		// ('*':[read,list,watch]) streams logs today and must be repaired
		// alongside its cluster-scope twin.
		"name = 'Project Viewer'",
		"UPDATE project_roles",
	} {
		if !strings.Contains(string(up), required) {
			t.Fatalf("up migration missing %q", required)
		}
	}
	// The reconcile repairs pod-log reads only. Granting exec anywhere would
	// hand an RCE-equivalent verb to a role the catalog never gave it to.
	if strings.Contains(string(up), `"exec"`) {
		t.Fatal("up migration must not grant the exec verb to any role")
	}
	// The authorized privilege reduction. 'Platform Operator' holds
	// clusters:update and zero pods rules; backfilling it would restore the
	// fleet-wide interactive shell this change deliberately removes.
	// 'Cluster Registrar' is the same shape ("cannot edit cluster workloads").
	//
	// 'Project Member' is also not backfilled, and for a DIFFERENT reason worth
	// keeping straight: its seed carries no clusters rule and no wildcard, so
	// CheckPermission(clusters, read) is false for it and it never had log
	// streaming to lose. Its divergence from project-member.yaml is real but
	// repairing it would be a privilege addition, not a compatibility repair.
	for _, forbidden := range []string{
		"name = 'Platform Operator'",
		"name IN ('Platform Operator'",
		"name = 'Cluster Registrar'",
		"name = 'Project Member'",
	} {
		if strings.Contains(string(up), forbidden) {
			t.Fatalf("up migration must not backfill %q", forbidden)
		}
	}
}
