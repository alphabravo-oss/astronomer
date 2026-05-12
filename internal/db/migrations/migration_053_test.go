package migrations_test

// Static content test for migration 053_cloud_credentials.
//
// CI's helm-test path covers the full Postgres apply against an empty
// database; this static check is the fast-feedback lint that protects
// the structural invariants the handler + worker depend on:
//
//   - cloud_credentials has the UNIQUE (project_id, name) constraint
//     the handler relies on for its 409 path.
//   - data_encrypted is NOT NULL (a row with no ciphertext is by
//     definition broken; the validate-on-write path rejects empty
//     blobs).
//   - target_refs has DEFAULT '[]' so a future ADD COLUMN against a
//     populated table doesn't break the JSONB array shape the worker
//     iterates over.
//   - cloud_credential_materializations has the UNIQUE
//     (credential_id, cluster_id, namespace) constraint the upsert
//     query depends on.
//   - The down file drops both tables (in the right order so the FK
//     doesn't dangle).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration053File(t *testing.T, name string) string {
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

func TestMigration_CloudCredentials_UpContent(t *testing.T) {
	up := loadMigration053File(t, "053_cloud_credentials.up.sql")

	for _, want := range []string{
		"CREATE TABLE cloud_credentials",
		"CREATE TABLE cloud_credential_materializations",
		"project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE",
		"name            VARCHAR(128) NOT NULL",
		"provider        VARCHAR(32) NOT NULL",
		"data_encrypted  TEXT NOT NULL",
		"target_refs     JSONB NOT NULL DEFAULT '[]'",
		"UNIQUE (project_id, name)",
		"CREATE INDEX idx_cloud_credentials_project ON cloud_credentials (project_id)",
		// Materializations table invariants.
		"credential_id       UUID NOT NULL REFERENCES cloud_credentials(id) ON DELETE CASCADE",
		"cluster_id          UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE",
		"namespace           VARCHAR(63) NOT NULL",
		"secret_name         VARCHAR(253) NOT NULL",
		"status              VARCHAR(16) NOT NULL DEFAULT 'pending'",
		"UNIQUE (credential_id, cluster_id, namespace)",
		"CREATE INDEX idx_cloud_credential_materializations_credential",
		"CREATE INDEX idx_cloud_credential_materializations_cluster",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing required text %q", want)
		}
	}
}

func TestMigration_CloudCredentials_DownContent(t *testing.T) {
	down := loadMigration053File(t, "053_cloud_credentials.down.sql")
	for _, want := range []string{
		"DROP TABLE IF EXISTS cloud_credential_materializations",
		"DROP TABLE IF EXISTS cloud_credentials",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("down migration missing required text %q", want)
		}
	}
	// The materializations table must drop FIRST (it has the FK to
	// cloud_credentials). Otherwise the drop would fail.
	posMat := strings.Index(down, "DROP TABLE IF EXISTS cloud_credential_materializations")
	posParent := strings.Index(down, "DROP TABLE IF EXISTS cloud_credentials;")
	if posMat < 0 || posParent < 0 {
		t.Fatalf("missing expected DROP statements")
	}
	if posMat > posParent {
		t.Errorf("materializations table dropped AFTER cloud_credentials; FK would block rollback")
	}
}
