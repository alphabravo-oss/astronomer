package migrations_test

// Content test for migration 087_orphan_template_steps_backfill.
//
// We don't run the SQL against Postgres here — helm-test covers that.
// What we pin is the data-fix shape: a future edit can't drop the
// existential clause that protects the genuinely-in-flight row, or
// silently switch to flipping every running row regardless of whether
// a later step exists.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMig087(t *testing.T, name string) string {
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

func TestOrphanTemplateStepsBackfill_UpContent(t *testing.T) {
	up := loadMig087(t, "087_orphan_template_steps_backfill.up.sql")
	mustContain := []string{
		"UPDATE cluster_registration_steps",
		"SET status        = 'failed'",
		"s.step_name = 'template_applying'",
		"s.status    = 'running'",
		// The existential clause is load-bearing — without it we would
		// also close the genuinely-in-flight step, breaking active
		// registrations.
		"EXISTS (",
		"FROM cluster_registration_steps s2",
		"s2.step_order > s.step_order",
	}
	for _, s := range mustContain {
		if !strings.Contains(up, s) {
			t.Errorf("up file missing required content:\n  %q", s)
		}
	}
}

func TestOrphanTemplateStepsBackfill_DownIsNoop(t *testing.T) {
	down := loadMig087(t, "087_orphan_template_steps_backfill.down.sql")
	// Down is intentionally a no-op (SELECT 1) — we cannot safely
	// reverse a data fix that didn't track which rows it touched.
	if !strings.Contains(down, "SELECT 1") {
		t.Errorf("down should be a no-op SELECT 1, got:\n%s", down)
	}
	// Guard against accidental destructive reverse.
	if strings.Contains(down, "UPDATE") || strings.Contains(down, "DELETE") {
		t.Errorf("down must not mutate data, got:\n%s", down)
	}
}
