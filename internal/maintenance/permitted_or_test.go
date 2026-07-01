package maintenance

import (
	"context"
	"testing"
	"time"
)

// permittedWindow builds a permitted-mode window scoped to one op type.
func permittedWindow(cron string, opType string) Window {
	return testWindow(func(w *Window) {
		w.Mode = ModePermitted
		w.CronOpen = cron
		w.DurationMinutes = 8 * 60 // 8h → covers the open period
		w.OperationTypes = []string{opType}
	})
}

// TestIsBlocked_MultiplePermittedWindows_OneActiveAllows is the
// regression for the AND-vs-OR bug: two permitted windows match the same
// op; one is active now (business hours) and one is inactive (weekend).
// The op must be ALLOWED because a permitted window is open. The old
// code returned blocked=true on the first inactive match.
func TestIsBlocked_MultiplePermittedWindows_OneActiveAllows(t *testing.T) {
	// Tuesday 2026-05-12 14:00 UTC — inside business-hours A, outside weekend B.
	now := time.Date(2026, 5, 12, 14, 0, 0, 0, time.UTC)
	businessHours := permittedWindow("0 9 * * 1-5", OpClusterTemplateApply) // Mon-Fri 09:00
	weekend := permittedWindow("0 9 * * 6,0", OpClusterTemplateApply)       // Sat/Sun 09:00

	// weekend first so the old first-inactive-wins logic would trip on it.
	ev := &fixedEvaluator{windows: []Window{weekend, businessHours}}
	blocked, _, err := IsBlocked(context.Background(), ev, OpClusterTemplateApply, nil, now)
	if err != nil {
		t.Fatalf("IsBlocked: %v", err)
	}
	if blocked {
		t.Fatal("op should be ALLOWED: a permitted window (business hours) is active now")
	}
}

// TestIsBlocked_MultiplePermittedWindows_NoneActiveBlocks preserves the
// blocking behavior: when NONE of the matching permitted windows is
// open, the op is still blocked.
func TestIsBlocked_MultiplePermittedWindows_NoneActiveBlocks(t *testing.T) {
	// Sunday 2026-05-10 03:00 UTC — outside business-hours A and outside
	// the weekend 09:00 window B (which opens later that day).
	now := time.Date(2026, 5, 10, 3, 0, 0, 0, time.UTC)
	businessHours := permittedWindow("0 9 * * 1-5", OpClusterTemplateApply)
	weekend := permittedWindow("0 9 * * 6,0", OpClusterTemplateApply)

	ev := &fixedEvaluator{windows: []Window{businessHours, weekend}}
	blocked, win, err := IsBlocked(context.Background(), ev, OpClusterTemplateApply, nil, now)
	if err != nil {
		t.Fatalf("IsBlocked: %v", err)
	}
	if !blocked {
		t.Fatal("op should be BLOCKED: no permitted window is currently open")
	}
	if win == nil {
		t.Fatal("blocked permitted result must return a matched window for the defer/refuse decision")
	}
}
