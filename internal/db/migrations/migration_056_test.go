package migrations_test

// Static content test for migration 056_fleet_operations.
//
// As with the other migration_*_test.go siblings, we DO NOT run the
// migration against Postgres — CI's helm-test path covers that via
// the migrate-job container. What we check here is the SHAPE of the
// SQL, so an unrelated future edit can't quietly:
//
//   - Drop the FK ON DELETE CASCADE on operation_id. Without it,
//     deleting a fleet_operations row would either fail or leave
//     dangling target rows.
//
//   - Drop the UNIQUE (operation_id, cluster_id) constraint. Without
//     it, the orchestrator's "create one target per matched cluster"
//     invariant breaks under a duplicate launch.
//
//   - Forget the partial status index. The orchestrator scans this
//     index every 10s — without the WHERE clause the index becomes
//     a full-table scan after years of completed runs accumulate.
//
//   - Drop the CHECK constraints. Bad string values from a bug
//     elsewhere would otherwise corrupt the row and the orchestrator's
//     switch statements would silently skip the row.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration056File(t *testing.T, name string) string {
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

func TestMigration_FleetOperations_UpContent(t *testing.T) {
	up := loadMigration056File(t, "056_fleet_operations.up.sql")

	for _, want := range []string{
		"CREATE TABLE fleet_operations",
		"CREATE TABLE fleet_operation_targets",
		// CHECK constraints — the orchestrator's state machine
		// silently breaks if anyone removes them and a bad string
		// lands in the row.
		"CONSTRAINT strategy_valid CHECK",
		"CONSTRAINT on_error_valid CHECK",
		"CONSTRAINT status_valid",
		// Cascade so deleting a fleet_operation cleans up its
		// targets — without this a CASCADE-less drop would leave
		// dangling target rows that the orchestrator would still
		// try to poll.
		"REFERENCES fleet_operations(id) ON DELETE CASCADE",
		// One row per (operation, cluster) — load-bearing for the
		// orchestrator's idempotency guarantee.
		"UNIQUE (operation_id, cluster_id)",
		// Partial index on the non-terminal statuses keeps the
		// orchestrator's polling scan tiny.
		"CREATE INDEX idx_fleet_operations_status",
		"WHERE status IN ('pending','running')",
		// Targets dispatch index.
		"CREATE INDEX idx_fleet_operation_targets_op",
		// Aggregate counters — read endpoints rely on these being
		// kept in lockstep with the targets table by the
		// orchestrator's UpdateFleetOperationCounters call.
		"total_clusters     INTEGER NOT NULL DEFAULT 0",
		"completed_clusters INTEGER NOT NULL DEFAULT 0",
		"failed_clusters    INTEGER NOT NULL DEFAULT 0",
		"skipped_clusters   INTEGER NOT NULL DEFAULT 0",
		// User audit trail — SET NULL so deleting the creator
		// doesn't blow away the operation history.
		"REFERENCES users(id) ON DELETE SET NULL",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing required text %q", want)
		}
	}
}

func TestMigration_FleetOperations_DownContent(t *testing.T) {
	down := loadMigration056File(t, "056_fleet_operations.down.sql")

	for _, want := range []string{
		"DROP TABLE IF EXISTS fleet_operation_targets",
		"DROP TABLE IF EXISTS fleet_operations",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("down migration missing required text %q", want)
		}
	}
	// Drop order: targets first (FK into operations), THEN operations.
	posTargets := strings.Index(down, "DROP TABLE IF EXISTS fleet_operation_targets")
	posOps := strings.Index(down, "DROP TABLE IF EXISTS fleet_operations")
	if posTargets < 0 || posOps < 0 {
		t.Fatalf("missing one of the expected DROP statements")
	}
	if posOps < posTargets {
		t.Fatalf("down migration drops operations before targets")
	}
}
