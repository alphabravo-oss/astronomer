package migrations_test

// Static content test for migration 057_maintenance_windows.
//
// CI's helm-test path covers the full Postgres apply against an empty
// database; this static check is the fast-feedback lint that protects
// the structural invariants the handler + dispatcher worker depend on:
//
//   - maintenance_windows has UNIQUE(name) (handler 409 path on POST).
//   - The mode + on_block CHECK constraints reject typos at insert
//     time (rather than letting a corrupt row break the evaluator).
//   - duration_minutes + cron_open default sensibly so a future ADD
//     COLUMN against a populated table doesn't break.
//   - deferred_operations cascades on window deletion (operator
//     deleting a window implicitly cancels its queued ops).
//   - The pending-rows partial index keeps the dispatcher's scan
//     cheap when the table grows.
//   - The down file drops deferred_operations FIRST so the FK to
//     maintenance_windows doesn't dangle on rollback.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration057File(t *testing.T, name string) string {
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

func TestMigration_MaintenanceWindows_UpContent(t *testing.T) {
	up := loadMigration057File(t, "057_maintenance_windows.up.sql")

	for _, want := range []string{
		"CREATE TABLE maintenance_windows",
		"CREATE TABLE deferred_operations",
		"name            VARCHAR(128) NOT NULL UNIQUE",
		"mode            VARCHAR(16) NOT NULL DEFAULT 'blackout'",
		"cron_open       VARCHAR(64) NOT NULL",
		"duration_minutes INTEGER NOT NULL DEFAULT 60",
		"timezone        VARCHAR(64) NOT NULL DEFAULT 'UTC'",
		"cluster_selector JSONB NOT NULL DEFAULT '{}'",
		"operation_types JSONB NOT NULL DEFAULT '[]'",
		"on_block        VARCHAR(16) NOT NULL DEFAULT 'refuse'",
		"enabled         BOOLEAN NOT NULL DEFAULT true",
		"CONSTRAINT mode_valid CHECK (mode IN ('blackout','permitted'))",
		"CONSTRAINT on_block_valid CHECK (on_block IN ('refuse','defer'))",
		// Deferred operations table invariants.
		"window_id       UUID NOT NULL REFERENCES maintenance_windows(id) ON DELETE CASCADE",
		"operation_type  VARCHAR(64) NOT NULL",
		"target_cluster_id UUID REFERENCES clusters(id) ON DELETE CASCADE",
		"target_project_id UUID REFERENCES projects(id) ON DELETE CASCADE",
		"status          VARCHAR(16) NOT NULL DEFAULT 'pending'",
		// Indexes that the evaluator + dispatcher rely on.
		"CREATE INDEX idx_maintenance_windows_enabled",
		"CREATE INDEX idx_deferred_operations_pending ON deferred_operations (status, deferred_until) WHERE status = 'pending'",
		"CREATE INDEX idx_deferred_operations_window",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing required text %q", want)
		}
	}
}

func TestMigration_MaintenanceWindows_DownContent(t *testing.T) {
	down := loadMigration057File(t, "057_maintenance_windows.down.sql")
	for _, want := range []string{
		"DROP TABLE IF EXISTS deferred_operations",
		"DROP TABLE IF EXISTS maintenance_windows",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("down migration missing required text %q", want)
		}
	}
	// deferred_operations must drop FIRST (it FKs to
	// maintenance_windows). Otherwise the drop would fail.
	posDeferred := strings.Index(down, "DROP TABLE IF EXISTS deferred_operations")
	posParent := strings.Index(down, "DROP TABLE IF EXISTS maintenance_windows")
	if posDeferred < 0 || posParent < 0 {
		t.Fatalf("missing expected DROP statements")
	}
	if posDeferred > posParent {
		t.Errorf("deferred_operations dropped AFTER maintenance_windows; FK would block rollback")
	}
}
