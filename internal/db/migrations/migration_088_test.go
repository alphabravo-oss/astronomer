package migrations_test

// Content test for migration 088_decommissioned_status_backfill.
//
// The fix the migration backs up lives in queries/clusters.sql
// (UpdateClusterStatus now guards on decommissioned_at IS NULL). The
// migration itself is a one-shot data fix; we pin the shape so a
// future edit can't silently broaden the WHERE clause.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMig088(t *testing.T, name string) string {
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

func TestDecommissionedStatusBackfill_UpContent(t *testing.T) {
	up := loadMig088(t, "088_decommissioned_status_backfill.up.sql")
	mustContain := []string{
		"UPDATE clusters",
		"SET status = 'decommissioned'",
		"WHERE decommissioned_at IS NOT NULL",
		"AND status != 'decommissioned'",
	}
	for _, s := range mustContain {
		if !strings.Contains(up, s) {
			t.Errorf("up file missing required content:\n  %q", s)
		}
	}
}

func TestDecommissionedStatusBackfill_DownIsNoop(t *testing.T) {
	down := loadMig088(t, "088_decommissioned_status_backfill.down.sql")
	if !strings.Contains(down, "SELECT 1") {
		t.Errorf("down should be a no-op SELECT 1, got:\n%s", down)
	}
	if strings.Contains(down, "UPDATE") || strings.Contains(down, "DELETE") {
		t.Errorf("down must not mutate data, got:\n%s", down)
	}
}
