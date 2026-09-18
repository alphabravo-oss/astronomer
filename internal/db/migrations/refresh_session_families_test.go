package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestRefreshSessionMigrationStoresOnlyHashesAndSerializesRotation(t *testing.T) {
	raw, err := os.ReadFile("047_refresh_session_families.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, required := range []string{
		"family_hash bytea PRIMARY KEY",
		"jti_hash bytea PRIMARY KEY",
		"octet_length(family_hash) = 32",
		"octet_length(jti_hash) = 32",
		"consumed_at timestamptz",
		"replaced_by_jti_hash bytea",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("refresh-session migration missing %q", required)
		}
	}
	for _, forbidden := range []string{"family_id uuid", "jti character varying", "refresh_token text"} {
		if strings.Contains(sql, forbidden) {
			t.Errorf("refresh-session migration stores raw session material %q", forbidden)
		}
	}
}
