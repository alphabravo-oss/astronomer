package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestUserPreferencesMigrationIsTypedAndUserOwned(t *testing.T) {
	up, err := os.ReadFile("033_user_preferences.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	schema := string(up)
	for _, required := range []string{
		"user_id uuid PRIMARY KEY REFERENCES public.users(id) ON DELETE CASCADE",
		"CHECK (theme IN ('light', 'dark', 'system'))",
		"CHECK (table_density IN ('compact', 'comfortable'))",
		"CHECK (time_format IN ('locale', '12h', '24h'))",
		"jsonb_array_length(favorites) <= 12",
	} {
		if !strings.Contains(schema, required) {
			t.Errorf("migration missing %q", required)
		}
	}
}
