package migrations_test

import (
	"os"
	"strings"
	"testing"
)

// Migration 146 gives monitoring_backends the Fernet envelope that migration
// 145 gave helm_repositories and explicitly deferred for this table.
//
// Same two failure modes are asserted as for 145, for the same reasons: it
// must not touch existing rows (a migration has no Fernet key, so any attempt
// to "convert" them writes something wrong and breaks monitoring on upgrade),
// and the column must be a TEXT column whose name contains "encrypted" —
// exactly the shape cmd/keyrotate/coverage_test.go recognises. A ciphertext
// column keyrotate does not sweep becomes permanently undecryptable on the
// next key rotation, and for this table that surfaces as "the monitoring
// backend went degraded", not as "a credential was lost".
func TestMigration146MonitoringBackendAuthConfigEncrypted(t *testing.T) {
	up, err := os.ReadFile("146_monitoring_backend_auth_config_encrypted.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	const required = "ADD COLUMN IF NOT EXISTS auth_config_encrypted TEXT NOT NULL DEFAULT ''"
	if !strings.Contains(string(up), required) {
		t.Fatalf("up migration missing %q", required)
	}

	// Additive DDL only. Existing rows keep auth_config_encrypted = '' and
	// stay authoritative in auth_config until
	// security:migrate_plaintext_credentials seals them.
	//
	// Needles must be UPPERCASE: the haystack is ToUpper'd, so a mixed-case
	// needle can never match and the check silently passes forever.
	for _, forbidden := range []string{
		"UPDATE MONITORING_BACKENDS",
		"DELETE FROM",
		"DROP COLUMN",
		"ALTER COLUMN",
	} {
		if strings.Contains(strings.ToUpper(string(up)), forbidden) {
			t.Fatalf("up migration must be additive only, found %q", forbidden)
		}
	}

	// The whole reason this column was deferred out of 145 is that it is a
	// mixed credential/config bag, so the split has to be written down where
	// the next person changing it will see it. Naming the allow-listed keys is
	// the difference between "moved a key out of the projection" being a
	// reviewable decision and being an outage.
	for _, key := range []string{
		"operationPolicies",
		"sharedThanos",
		"sharedAlertmanager",
		"sharedAlertingAssets",
	} {
		if !strings.Contains(string(up), key) {
			t.Fatalf("up migration does not document the non-secret key %q that stays in the clear", key)
		}
	}
	if !strings.Contains(string(up), "does NOT do") {
		t.Fatal("up migration is missing its 'what this deliberately does NOT do' block")
	}

	down, err := os.ReadFile("146_monitoring_backend_auth_config_encrypted.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(down), "DROP COLUMN IF EXISTS auth_config_encrypted") {
		t.Fatal("down migration missing DROP COLUMN IF EXISTS auth_config_encrypted")
	}
	// The down path is lossy and must say so rather than pretend it can
	// restore a credential it has no key to decrypt.
	if !strings.Contains(strings.ToLower(string(down)), "lossy") {
		t.Fatal("down migration must state that dropping the envelope discards the credential")
	}
}
