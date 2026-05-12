package migrations_test

// Static content test for migration 051_tenant_quotas.
//
// We don't run the migration against Postgres in unit tests — the CI
// helm-test path covers that through the migrate-job container. What
// we DO check here is the shape of the SQL, so a well-meaning future
// edit can't accidentally:
//
//   - Drop the enforcement CHECK constraint (which is what makes the
//     soft/hard distinction operator-visible at the DB layer).
//   - Forget the ON DELETE SET DEFAULT on the projects / users FKs,
//     which would change "rollback a plan" from a soft fallback to
//     'free' into a hard "you can't delete this plan because rows
//     reference it" — a regression the in-handler 409 already
//     surfaces, but the FK is the suspenders.
//   - Skip the 'free' or 'global' seed row, both of which the handler
//     code path depends on (default plan + fleet-cap singleton).
//   - Forget the JSONB DEFAULT '{}' on quota_overrides, which would
//     leave the column NULL and break the override-merge code in the
//     enforcer (effectiveLimit assumes non-nil blobs).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration051File(t *testing.T, name string) string {
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

func TestMigration_TenantQuotas_UpContent(t *testing.T) {
	up := loadMigration051File(t, "051_tenant_quotas.up.sql")

	for _, want := range []string{
		"CREATE TABLE quota_plans",
		"enforcement                 VARCHAR(8) NOT NULL DEFAULT 'hard'",
		"CONSTRAINT enforcement_valid CHECK (enforcement IN ('soft', 'hard'))",
		"max_clusters_per_project    INTEGER NOT NULL DEFAULT 0",
		"max_total_clusters          INTEGER NOT NULL DEFAULT 0",
		// Seeded plan rows.
		"('free',",
		"('team',",
		"('enterprise',",
		"('global',",
		// Projects + users get the new columns with safe defaults.
		"ADD COLUMN quota_plan      VARCHAR(64) NOT NULL DEFAULT 'free' REFERENCES quota_plans(name) ON DELETE SET DEFAULT",
		"ADD COLUMN quota_overrides JSONB NOT NULL DEFAULT '{}'",
		// Lookup indexes for the FK columns — required so the
		// fleet-usage aggregation doesn't sequential-scan.
		"CREATE INDEX idx_projects_quota_plan",
		"CREATE INDEX idx_users_quota_plan",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing required text %q", want)
		}
	}
}

func TestMigration_TenantQuotas_DownContent(t *testing.T) {
	down := loadMigration051File(t, "051_tenant_quotas.down.sql")

	for _, want := range []string{
		"DROP INDEX IF EXISTS idx_users_quota_plan",
		"DROP INDEX IF EXISTS idx_projects_quota_plan",
		"DROP COLUMN IF EXISTS quota_overrides",
		"DROP COLUMN IF EXISTS quota_plan",
		"DROP TABLE IF EXISTS quota_plans",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("down migration missing required text %q", want)
		}
	}

	// Drop order matters: the FK columns must come off before the
	// quota_plans table is removed.
	posUsersCol := strings.Index(down, "ALTER TABLE users    DROP COLUMN IF EXISTS quota_plan")
	posTable := strings.Index(down, "DROP TABLE IF EXISTS quota_plans")
	if posUsersCol < 0 || posTable < 0 {
		t.Fatalf("missing expected DROP statements")
	}
	if posTable < posUsersCol {
		t.Errorf("quota_plans table dropped before its FK columns; rollback would fail")
	}
}
