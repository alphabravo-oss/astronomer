package migrations_test

import (
	"os"
	"strings"
	"testing"
)

// Migration 145 gives helm_repositories a Fernet envelope for the chart-repo
// credential that has been bare JSONB since 001.
//
// The two things that would make this change worse than not doing it are both
// asserted here: it must not touch existing rows (a migration has no Fernet
// key, so any attempt to "convert" them writes something wrong and breaks
// every authenticated repository on upgrade), and the column must be a TEXT
// column whose name contains "encrypted" — that is exactly the shape
// cmd/keyrotate/coverage_test.go recognises, and a ciphertext column keyrotate
// does not sweep becomes permanently undecryptable on the next key rotation.
func TestMigration145HelmRepositoryAuthConfigEncrypted(t *testing.T) {
	up, err := os.ReadFile("145_helm_repository_auth_config_encrypted.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	const required = "ADD COLUMN IF NOT EXISTS auth_config_encrypted TEXT NOT NULL DEFAULT ''"
	if !strings.Contains(string(up), required) {
		t.Fatalf("up migration missing %q", required)
	}

	// Additive DDL only. The migration cannot encrypt (no access to the Go
	// encryptor), so an UPDATE here could only either blank a live credential
	// or write plaintext into the ciphertext column and pretend. Existing rows
	// keep auth_config_encrypted = '' and stay authoritative in auth_config
	// until security:migrate_plaintext_credentials seals them.
	//
	// Needles must be UPPERCASE: the haystack is ToUpper'd, so a mixed-case
	// needle can never match and the check silently passes forever.
	for _, forbidden := range []string{
		"UPDATE HELM_REPOSITORIES",
		"DELETE FROM",
		"DROP COLUMN",
		"ALTER COLUMN",
	} {
		if strings.Contains(strings.ToUpper(string(up)), forbidden) {
			t.Fatalf("up migration must be additive only, found %q", forbidden)
		}
	}

	down, err := os.ReadFile("145_helm_repository_auth_config_encrypted.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(down), "DROP COLUMN IF EXISTS auth_config_encrypted") {
		t.Fatal("down migration missing DROP COLUMN IF EXISTS auth_config_encrypted")
	}
	// The down path is lossy and must say so rather than pretend it can
	// restore the credential it has no key to decrypt.
	if !strings.Contains(strings.ToLower(string(down)), "lossy") {
		t.Fatal("down migration must state that dropping the envelope discards the credentials")
	}
}
