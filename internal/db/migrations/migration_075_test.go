package migrations_test

// Static content test for migration 075_seed_helm_repositories.
//
// CI's helm-test path covers the full Postgres apply against an empty
// database; this static check is the fast-feedback lint that protects
// the structural invariants the reconciler + first-boot sync rely on:
//
//   - All three seeded names (bitnami, aqua, jetstack) are present.
//   - All three upstream URLs are present and unmodified.
//   - ON CONFLICT (name) DO NOTHING is in place so re-running the
//     migration is idempotent and never overrides an operator's
//     customized row.
//   - The down file deletes ONLY the three named rows so operator-added
//     repos survive a downgrade.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration075File(t *testing.T, name string) string {
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

func TestCatalogSeed_Migration075_UpContent(t *testing.T) {
	up := loadMigration075File(t, "075_seed_helm_repositories.up.sql")

	for _, want := range []string{
		// Insert target + the three seeded names.
		"INSERT INTO helm_repositories",
		"'bitnami'",
		"'aqua'",
		"'jetstack'",
		// Upstream URLs operators expect to see (mismatches here would
		// silently send the reconciler to the wrong index.yaml).
		"https://charts.bitnami.com/bitnami",
		"https://aquasecurity.github.io/helm-charts",
		"https://charts.jetstack.io",
		// Idempotency / operator-customization safety.
		"ON CONFLICT (name) DO NOTHING",
		// Defaults must mark the rows enabled with no auth so the
		// catalog:sync worker can crawl them with no further config.
		"'helm'",
		"'none'",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("075 up migration missing required text %q", want)
		}
	}

	// Anti-patterns: the migration MUST NOT do anything other than seed
	// the three named rows. No ALTER TABLE, no new column, no DROP. The
	// down file MUST NOT touch operator-added rows.
	for _, bad := range []string{
		"ALTER TABLE helm_repositories",
		"DROP TABLE",
		"DELETE FROM helm_repositories WHERE name NOT IN",
	} {
		if strings.Contains(up, bad) {
			t.Errorf("075 up migration contains forbidden text %q", bad)
		}
	}
}

func TestCatalogSeed_Migration075_DownContent(t *testing.T) {
	down := loadMigration075File(t, "075_seed_helm_repositories.down.sql")

	// The down MUST delete only the three named rows.
	if !strings.Contains(down, "DELETE FROM helm_repositories WHERE name IN ('bitnami', 'aqua', 'jetstack')") {
		t.Errorf("075 down migration must delete by name, got: %s", down)
	}
	// And MUST NOT drop the table or remove anything broader.
	for _, bad := range []string{
		"DROP TABLE",
		"TRUNCATE",
		"DELETE FROM helm_charts",
	} {
		if strings.Contains(down, bad) {
			t.Errorf("075 down migration contains forbidden text %q", bad)
		}
	}
}

// TestCatalogSeed_Migration075_DoesNotOverrideExisting is a contract
// check: the ON CONFLICT clause is what guarantees a pre-existing
// bitnami row with an operator-edited URL (e.g. a private mirror)
// survives the migration. The actual behavior is enforced by Postgres
// when the SQL runs; this test guards the clause from being dropped
// during future refactors.
func TestCatalogSeed_Migration075_DoesNotOverrideExisting(t *testing.T) {
	up := loadMigration075File(t, "075_seed_helm_repositories.up.sql")
	if !strings.Contains(up, "ON CONFLICT (name) DO NOTHING") {
		t.Fatalf("075 up migration must use ON CONFLICT (name) DO NOTHING so operator-edited rows survive re-runs; got: %s", up)
	}
	// Belt-and-suspenders: DO UPDATE would silently replace operator
	// edits. The migration MUST stay on DO NOTHING.
	if strings.Contains(up, "DO UPDATE") {
		t.Fatalf("075 up migration must NOT use ON CONFLICT ... DO UPDATE — that would override operator-customized rows")
	}
}
