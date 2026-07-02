package migrations_test

import (
	"strings"
	"testing"
)

// TestMigration131AddsEventTimeIndex asserts the retention-prune support index
// is created on apiserver_audit_events(event_time). Without it the daily
// PruneApiserverAuditEventsBefore DELETE (WHERE event_time < cutoff) cannot use
// the composite (cluster_id, event_time DESC) index from migration 112 — a
// leading-column mismatch — and degrades to a full sequential scan on a
// fleet-wide, million-row table.
func TestMigration131AddsEventTimeIndex(t *testing.T) {
	up := loadMigrationFile(t, "131_apiserver_audit_events_event_time_index.up.sql")
	for _, needle := range []string{
		"CREATE INDEX IF NOT EXISTS idx_apiserver_audit_events_event_time",
		"ON apiserver_audit_events (event_time)",
	} {
		if !strings.Contains(up, needle) {
			t.Fatalf("migration 131 up missing %q", needle)
		}
	}
}

func TestMigration131DownDropsEventTimeIndex(t *testing.T) {
	down := loadMigrationFile(t, "131_apiserver_audit_events_event_time_index.down.sql")
	if !strings.Contains(down, "DROP INDEX IF EXISTS idx_apiserver_audit_events_event_time") {
		t.Fatalf("migration 131 down missing DROP INDEX")
	}
}
