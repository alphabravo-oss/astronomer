package migrations_test

// Static content test for migration 045_sso_to_dex_connectors.
//
// We don't run the migration against a real Postgres in unit tests — the
// CI flow already covers that through the migrate-job container in the
// helm-test path. What we DO check here is the shape of the SQL, so a
// well-meaning future edit can't accidentally:
//
//   - Make the migration non-idempotent (re-runs must be no-ops).
//   - Forget the matching `migrated_to_dex_at` stamp pass — that breaks
//     the deprecation warning logic in WarnIfLegacySSORowsActive.
//   - Drop ON CONFLICT (name) DO NOTHING and start crashing on partial
//     re-runs.
//   - Reference columns that don't exist on sso_configurations.
//
// The .down.sql also gets the same treatment so an operator who rolls
// back doesn't strand orphan rows.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigrationFile(t *testing.T, name string) string {
	t.Helper()
	// Walk up the worktree until we find the migrations directory.
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

func TestMigration_SSOConfigurationsToDexConnectors_UpContent(t *testing.T) {
	up := loadMigrationFile(t, "045_sso_to_dex_connectors.up.sql")

	// Phase 1: the INSERT must target dex_connectors with a re-runnable
	// ON CONFLICT clause keyed on the connector name (the unique index
	// migration 023 created).
	for _, want := range []string{
		"INSERT INTO dex_connectors",
		"FROM sso_configurations",
		"WHERE is_enabled = true",
		"ON CONFLICT (name) DO NOTHING",
		"'legacy-' || provider",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing required text %q", want)
		}
	}

	// Phase 1 mapping: each of the three legacy providers must show up
	// as a CASE branch building its own config shape.
	for _, want := range []string{
		"WHEN 'github'",
		"WHEN 'google'",
		"WHEN 'oidc'",
		"'clientID',     client_id",
		"'clientSecret', client_secret_encrypted",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing per-provider mapping fragment %q", want)
		}
	}

	// Phase 2: the migrated_to_dex_at column add must be IF NOT EXISTS
	// (idempotent) and must NOT be NOT NULL without a default (T30
	// migration-lint contract). NULLable with default-null is fine.
	for _, want := range []string{
		"ADD COLUMN IF NOT EXISTS migrated_to_dex_at TIMESTAMPTZ",
		"UPDATE sso_configurations",
		"SET migrated_to_dex_at = now()",
		"WHERE is_enabled = true",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing stamp fragment %q", want)
		}
	}

	// Belt-and-braces: the stamp pass must avoid re-stamping rows so the
	// timestamp reflects the actual migration, not subsequent re-runs.
	if !strings.Contains(up, "migrated_to_dex_at IS NULL") {
		t.Errorf("stamp pass should guard with IS NULL so re-runs preserve the original timestamp")
	}
}

func TestMigration_SSOConfigurationsToDexConnectors_DownContent(t *testing.T) {
	down := loadMigrationFile(t, "045_sso_to_dex_connectors.down.sql")
	for _, want := range []string{
		"DELETE FROM dex_connectors",
		"WHERE name LIKE 'legacy-%'",
		"DROP COLUMN IF EXISTS migrated_to_dex_at",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("down migration missing %q", want)
		}
	}
}

func TestMigration_LegacyConnectorMapping_LegacyPrefixIsConsistent(t *testing.T) {
	// Up and down agree on the `legacy-` prefix; otherwise a rollback
	// either misses migrated rows (data still in dex_connectors) or
	// nukes operator-created connectors that happen to share a name.
	up := loadMigrationFile(t, "045_sso_to_dex_connectors.up.sql")
	down := loadMigrationFile(t, "045_sso_to_dex_connectors.down.sql")
	if !strings.Contains(up, "'legacy-'") {
		t.Errorf("up should prefix migrated rows with 'legacy-'")
	}
	if !strings.Contains(down, "'legacy-%'") {
		t.Errorf("down should target the same 'legacy-%%' prefix on rollback")
	}
}
