package migrations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration127File(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// TestApiserverAuditEventsClusterFKMigrationContent asserts migration 127 adds
// the missing clusters(id) foreign key with ON DELETE CASCADE to
// apiserver_audit_events (migration 112 shipped without one, leaking rows on a
// hard cluster delete), reaps pre-existing orphans first so the constraint
// validates, and that the down cleanly drops the constraint.
func TestApiserverAuditEventsClusterFKMigrationContent(t *testing.T) {
	up := loadMigration127File(t, "127_apiserver_audit_events_cluster_fk.up.sql")
	down := loadMigration127File(t, "127_apiserver_audit_events_cluster_fk.down.sql")

	// Orphans must be reaped before the constraint is added, or ADD CONSTRAINT
	// fails to validate on any DB that already has orphaned audit rows.
	orphanIdx := strings.Index(up, "DELETE FROM apiserver_audit_events")
	if orphanIdx < 0 {
		t.Error("127 up must reap orphaned apiserver_audit_events rows before adding the FK")
	}

	// The FK itself: cluster_id -> clusters(id) ON DELETE CASCADE.
	if !strings.Contains(up, "FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE") {
		t.Error("127 up must add cluster_id FK to clusters(id) with ON DELETE CASCADE")
	}
	fkIdx := strings.Index(up, "ADD CONSTRAINT apiserver_audit_events_cluster_id_fkey")
	if fkIdx < 0 {
		t.Error("127 up must ADD CONSTRAINT apiserver_audit_events_cluster_id_fkey")
	}
	if orphanIdx >= 0 && fkIdx >= 0 && orphanIdx > fkIdx {
		t.Error("127 up must reap orphans BEFORE adding the FK constraint")
	}

	if !strings.Contains(down, "DROP CONSTRAINT IF EXISTS apiserver_audit_events_cluster_id_fkey") {
		t.Error("127 down must DROP CONSTRAINT IF EXISTS apiserver_audit_events_cluster_id_fkey")
	}
}
