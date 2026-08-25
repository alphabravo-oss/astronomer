package migrations_test

import (
	"strings"
	"testing"
)

func TestManagementBackupSagaMigrationContracts(t *testing.T) {
	up := readMigration(t, "021_management_backup_sagas.up.sql")
	for _, required := range []string{"desired_generation", "applied_generation", "desired_state", "reconcile_status", "last_error", "last_reconciled_at"} {
		if !strings.Contains(up, required) {
			t.Fatalf("migration missing %q", required)
		}
	}
	if !strings.Contains(up, "'retrying'") {
		t.Fatal("management backup reconciliation has no truthful retrying state")
	}
	down := readMigration(t, "021_management_backup_sagas.down.sql")
	if !strings.Contains(down, "DROP COLUMN IF EXISTS desired_generation") {
		t.Fatal("down migration does not remove desired-state columns")
	}
}
