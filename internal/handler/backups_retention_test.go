package handler

import (
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestSelectBackupsToPrune(t *testing.T) {
	// backups must be newest-first (as ListBackups returns them).
	bk := func(name, status string) sqlc.Backup {
		return sqlc.Backup{ID: uuid.New(), VeleroBackupName: name, Status: status}
	}
	backups := []sqlc.Backup{
		bk("nightly-5", "completed"), // newest
		bk("nightly-4", "completed"),
		bk("nightly-3", "failed"), // not completed → ignored for count
		bk("nightly-2", "completed"),
		bk("nightly-1", "completed"), // oldest
		bk("hourly-9", "completed"),  // different schedule
		bk("adhoc-xyz", "completed"), // no matching schedule prefix
	}
	schedules := []sqlc.BackupSchedule{
		{VeleroScheduleName: "nightly", RetentionCount: 2},
		{VeleroScheduleName: "hourly", RetentionCount: 5}, // only 1 backup → nothing to prune
		{VeleroScheduleName: "", RetentionCount: 3},       // unnamed → skipped
		{VeleroScheduleName: "weekly", RetentionCount: 0}, // no count cap → skipped
	}

	got := selectBackupsToPrune(schedules, backups)

	// nightly keeps the 2 newest COMPLETED (nightly-5, nightly-4); the failed
	// nightly-3 doesn't count, so the prune set is the older completed ones:
	// nightly-2 and nightly-1. hourly/adhoc/weekly untouched.
	wantNames := map[string]bool{"nightly-2": true, "nightly-1": true}
	if len(got) != len(wantNames) {
		t.Fatalf("pruned %d backups, want %d: %+v", len(got), len(wantNames), names(got))
	}
	for _, b := range got {
		if !wantNames[b.VeleroBackupName] {
			t.Errorf("unexpectedly pruned %q", b.VeleroBackupName)
		}
	}
}

func names(bs []sqlc.Backup) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.VeleroBackupName
	}
	return out
}
