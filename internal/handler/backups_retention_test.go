package handler

import (
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestSelectBackupsToPrune(t *testing.T) {
	// backups must be newest-first (as ListBackups returns them). Names follow
	// Velero's schedule convention "<scheduleName>-<14-digit-timestamp>".
	bk := func(name, status string) sqlc.Backup {
		return sqlc.Backup{ID: uuid.New(), VeleroBackupName: name, Status: status}
	}
	backups := []sqlc.Backup{
		bk("nightly-20260105000000", "completed"), // newest
		bk("nightly-20260104000000", "completed"),
		bk("nightly-20260103000000", "failed"), // not completed → ignored for count
		bk("nightly-20260102000000", "completed"),
		bk("nightly-20260101000000", "completed"), // oldest
		bk("hourly-20260105090000", "completed"),  // different schedule
		bk("adhoc-abc123def456", "completed"),     // no matching schedule
	}
	schedules := []sqlc.BackupSchedule{
		{VeleroScheduleName: "nightly", RetentionCount: 2},
		{VeleroScheduleName: "hourly", RetentionCount: 5}, // only 1 backup → nothing to prune
		{VeleroScheduleName: "", RetentionCount: 3},       // unnamed → skipped
		{VeleroScheduleName: "weekly", RetentionCount: 0}, // no count cap → skipped
	}

	got := selectBackupsToPrune(schedules, backups)

	// nightly keeps the 2 newest COMPLETED; the failed one doesn't count, so
	// the prune set is the older completed ones. hourly/adhoc/weekly untouched.
	wantNames := map[string]bool{"nightly-20260102000000": true, "nightly-20260101000000": true}
	if len(got) != len(wantNames) {
		t.Fatalf("pruned %d backups, want %d: %+v", len(got), len(wantNames), names(got))
	}
	for _, b := range got {
		if !wantNames[b.VeleroBackupName] {
			t.Errorf("unexpectedly pruned %q", b.VeleroBackupName)
		}
	}
}

// TestSelectBackupsToPrune_PrefixCollision is the regression for the data-loss
// bug: when two schedule names are prefix-related ("daily" vs "daily-critical"),
// the "daily" schedule must NOT claim (and prune) "daily-critical"'s backups.
// A bare name-prefix match would attribute all four backups to "daily" and
// prune the two oldest — deleting one of daily-critical's backups.
func TestSelectBackupsToPrune_PrefixCollision(t *testing.T) {
	bk := func(name string) sqlc.Backup {
		return sqlc.Backup{ID: uuid.New(), VeleroBackupName: name, Status: "completed"}
	}
	backups := []sqlc.Backup{
		bk("daily-critical-20260104000000"), // newest
		bk("daily-20260104000000"),
		bk("daily-critical-20260101000000"),
		bk("daily-20260101000000"), // oldest
	}
	schedules := []sqlc.BackupSchedule{
		{VeleroScheduleName: "daily", RetentionCount: 1},
		{VeleroScheduleName: "daily-critical", RetentionCount: 1},
	}

	got := selectBackupsToPrune(schedules, backups)

	// Each schedule keeps its own newest; each prunes its own older one. No
	// backup may be attributed to the OTHER schedule.
	want := map[string]bool{
		"daily-20260101000000":          true,
		"daily-critical-20260101000000": true,
	}
	if len(got) != len(want) {
		t.Fatalf("pruned %d, want %d: %+v", len(got), len(want), names(got))
	}
	for _, b := range got {
		if !want[b.VeleroBackupName] {
			t.Errorf("cross-schedule attribution: unexpectedly pruned %q", b.VeleroBackupName)
		}
	}
}

func TestIsScheduleOwnedBackup(t *testing.T) {
	cases := []struct {
		backup   string
		schedule string
		want     bool
	}{
		{"nightly-20260101000000", "nightly", true},
		{"daily-critical-20260101000000", "daily-critical", true},
		{"daily-critical-20260101000000", "daily", false}, // prefix collision rejected
		{"nightly-5", "nightly", false},                   // not a 14-digit timestamp
		{"nightly-2026010100000", "nightly", false},       // 13 digits
		{"nightly-2026010100000x", "nightly", false},      // 14 chars but not all digits
		{"adhoc-abc123def456", "nightly", false},          // wrong prefix
	}
	for _, c := range cases {
		if got := isScheduleOwnedBackup(c.backup, c.schedule); got != c.want {
			t.Errorf("isScheduleOwnedBackup(%q, %q) = %v, want %v", c.backup, c.schedule, got, c.want)
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
