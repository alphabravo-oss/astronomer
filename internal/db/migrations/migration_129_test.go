package migrations_test

import (
	"strings"
	"testing"
)

// Static content test for migration 129 (group-sync connector backfill).
// We don't run it against a live Postgres here (the helm migrate-job path
// covers that); we assert the SQL shape so a future edit can't silently:
//
//   - Backfill rows that are already stamped (must only touch NULLs → stays
//     idempotent and never clobbers live provenance).
//   - Attribute a wildcard/unknown legacy grant to a connector with no
//     matching NAMED-connector mapping (the EXISTS guard prevents
//     re-introducing the over-retention the reconciler fix closes).
//   - Backfill from a NULL user_idp_groups.connector_id.
func TestMigration129BackfillConnectorProvenance(t *testing.T) {
	up := loadMigrationFile(t, "129_group_sync_binding_backfill_connector.up.sql")

	// All three scoped binding tables get backfilled.
	for _, tbl := range []string{
		"UPDATE global_role_bindings",
		"UPDATE cluster_role_bindings",
		"UPDATE project_role_bindings",
	} {
		if !strings.Contains(up, tbl) {
			t.Fatalf("migration 129 missing %q", tbl)
		}
	}

	required := []string{
		// Only reconcile group_sync bindings...
		"b.source = 'group_sync'",
		// ...that are still unstamped (idempotent, no clobber).
		"b.group_sync_connector_id IS NULL",
		// Never derive a connector from a NULL snapshot.
		"uig.connector_id IS NOT NULL",
		// Guard: only attribute when a NAMED-connector mapping justifies it.
		"EXISTS (",
		"m.connector_id = uig.connector_id",
		"m.scope = 'global'",
		"m.scope = 'cluster'",
		"m.scope = 'project'",
		"m.cluster_id = b.cluster_id",
		"m.project_id = b.project_id",
	}
	for _, needle := range required {
		if !strings.Contains(up, needle) {
			t.Fatalf("migration 129 up missing %q", needle)
		}
	}

	// The guard must key the mapping match to the binding's role.
	if !strings.Contains(up, "m.role_id = b.role_id") {
		t.Fatalf("migration 129 up must match mapping role to binding role")
	}

	// Down must not clear the column (can't distinguish backfilled from
	// live-stamped rows); schema rollback is 128's job.
	down := loadMigrationFile(t, "129_group_sync_binding_backfill_connector.down.sql")
	if strings.Contains(down, "SET group_sync_connector_id") {
		t.Fatalf("migration 129 down must not clear group_sync_connector_id (would corrupt live provenance)")
	}
}
