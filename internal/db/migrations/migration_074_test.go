package migrations_test

// Static content test for migration 074_platform_baseline.
//
// CI's helm-test path covers the full Postgres apply against an empty
// database; this fast-feedback static check protects the structural
// invariants the cluster Create auto-attach hook + the platform-default
// template handler depend on:
//
//   - platform_configuration gets the default_cluster_template_id FK
//     column with ON DELETE SET NULL (so deleting a referenced
//     template doesn't break the singleton row).
//   - A cluster_templates seed named 'Platform baseline' lists all five
//     baseline chart slugs in the spec JSONB:
//         trivy-operator
//         kube-state-metrics
//         node-exporter
//         fluent-bit
//         cert-manager
//   - The seed UPDATE is guarded by `WHERE default_cluster_template_id
//     IS NULL` so re-running on a DB where the operator has already
//     picked a different default does NOT overwrite their choice
//     (idempotency + override-respect).
//   - The down file drops the column first (FK ordering) and only
//     deletes the seed when no cluster_template_applications still
//     reference it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration074File(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	path := filepath.Join(dir, name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestPlatformDefaultTemplate_MigrationSeedsPlatformBaselineTemplate
// (sprint test prefix) checks the up.sql lists every required chart
// slug + structural anchor.
func TestPlatformDefaultTemplate_MigrationSeedsPlatformBaselineTemplate(t *testing.T) {
	up := loadMigration074File(t, "074_platform_baseline.up.sql")

	for _, want := range []string{
		// Column add — exact NULL-default shape so the lint script in
		// scripts/check-migrations.sh passes.
		"ALTER TABLE platform_configuration ADD COLUMN IF NOT EXISTS",
		"default_cluster_template_id UUID REFERENCES cluster_templates(id) ON DELETE SET NULL",
		// Seed row anchor — name is the stable UNIQUE key the handler
		// looks up.
		"'Platform baseline'",
		"ON CONFLICT (name) DO NOTHING",
		// Five baseline chart slugs the apply worker resolves at
		// reconcile time. The order is the operator UX order
		// (security → metrics → log → TLS).
		"'trivy-operator'",
		"'kube-state-metrics'",
		"'node-exporter'",
		"'fluent-bit'",
		"'cert-manager'",
		// Idempotent default-set — operator overrides survive a
		// re-run.
		"WHERE default_cluster_template_id IS NULL",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing required text %q", want)
		}
	}
}

// TestPlatformDefaultTemplate_MigrationDoesNotOverrideExistingDefault
// validates the WHERE clause that gates the UPDATE — the
// operator-override-respect rule.
func TestPlatformDefaultTemplate_MigrationDoesNotOverrideExistingDefault(t *testing.T) {
	up := loadMigration074File(t, "074_platform_baseline.up.sql")

	// The UPDATE must NOT be unconditional — re-running the migration
	// on a DB where the operator already picked a non-baseline template
	// must leave their choice intact. The guard is the WHERE
	// `default_cluster_template_id IS NULL` clause.
	if !strings.Contains(up, "WHERE default_cluster_template_id IS NULL") {
		t.Errorf("up migration UPDATE is missing the IS NULL guard; an operator's existing default would be clobbered on re-run")
	}

	// The cluster_templates seed must be ON CONFLICT DO NOTHING so a
	// re-run doesn't blow away operator changes to the seed row.
	if !strings.Contains(up, "ON CONFLICT (name) DO NOTHING") {
		t.Errorf("up migration is missing ON CONFLICT (name) DO NOTHING on the seed INSERT; a re-run would 23505")
	}
}

// TestPlatformDefaultTemplate_MigrationDownContent verifies the
// rollback order + the safe-delete guard.
func TestPlatformDefaultTemplate_MigrationDownContent(t *testing.T) {
	down := loadMigration074File(t, "074_platform_baseline.down.sql")

	for _, want := range []string{
		"ALTER TABLE platform_configuration DROP COLUMN IF EXISTS default_cluster_template_id",
		"DELETE FROM cluster_templates",
		"'Platform baseline'",
		// Safe-delete guard — don't blow away the row when clusters
		// still reference it.
		"cluster_template_applications",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("down migration missing required text %q", want)
		}
	}

	// The column drop must come BEFORE the row delete because of the
	// FK on platform_configuration.default_cluster_template_id.
	colDrop := strings.Index(down, "DROP COLUMN")
	rowDel := strings.Index(down, "DELETE FROM cluster_templates")
	if colDrop == -1 || rowDel == -1 {
		t.Fatalf("down migration missing column-drop or row-delete; got colDrop=%d rowDel=%d", colDrop, rowDel)
	}
	if colDrop > rowDel {
		t.Errorf("down migration drops the column AFTER deleting the seed; FK from platform_configuration would block the row delete")
	}
}
