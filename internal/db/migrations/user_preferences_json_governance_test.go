package migrations_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestUserPreferencesJSONGovernanceMigration(t *testing.T) {
	up := readMigration(t, "066_user_preferences_json_governance.up.sql")
	down := readMigration(t, "066_user_preferences_json_governance.down.sql")
	for _, column := range []string{"pinned_clusters", "starred_types"} {
		contract := fmt.Sprintf("'public', 'user_preferences', '%s', 1, 'array', 16777216, false, '{}', 'additive', 'platform'", column)
		if !strings.Contains(up, contract) {
			t.Errorf("missing preference contract: %s", column)
		}
		if !strings.Contains(down, "'"+column+"'") {
			t.Errorf("rollback does not remove contract: %s", column)
		}
	}
	for _, scope := range []string{"table_schema = 'public'", "table_name = 'user_preferences'", "column_name IN"} {
		if !strings.Contains(down, scope) {
			t.Errorf("rollback is not scoped by %s", scope)
		}
	}
	if strings.Contains(strings.ToUpper(down), "DROP TRIGGER") || strings.Contains(strings.ToUpper(down), "DELETE FROM PUBLIC.USER_PREFERENCES") {
		t.Fatal("rollback must preserve preference data and the shared writer trigger")
	}
}
