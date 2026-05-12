package maintenance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fixedEvaluator is an in-memory WindowEvaluator the tests use to skip
// the DB round-trip in *Evaluator.
type fixedEvaluator struct {
	windows []Window
	calls   int
}

func (f *fixedEvaluator) Windows(ctx context.Context) ([]Window, error) {
	f.calls++
	return f.windows, nil
}

// testWindow builds a Window with sensible defaults so each test only
// has to set the fields it cares about.
func testWindow(opts ...func(*Window)) Window {
	w := Window{
		ID:              uuid.New(),
		Name:            "test",
		Mode:            ModeBlackout,
		CronOpen:        "0 9 * * 1-5", // 9am Mon-Fri
		DurationMinutes: 8 * 60,        // 8h window → covers 9am-5pm
		Timezone:        "UTC",
		ClusterSelector: nil,
		OperationTypes:  nil,
		OnBlock:         OnBlockRefuse,
		Enabled:         true,
	}
	for _, opt := range opts {
		opt(&w)
	}
	return w
}

func TestEvaluator_BlackoutWithinWindow(t *testing.T) {
	// Tuesday 14:00 UTC — squarely inside the 9-17 window.
	now := time.Date(2026, 5, 12, 14, 0, 0, 0, time.UTC)
	ev := &fixedEvaluator{windows: []Window{testWindow()}}
	blocked, w, err := IsBlocked(context.Background(), ev, OpClusterDelete, nil, now)
	if err != nil {
		t.Fatalf("IsBlocked: %v", err)
	}
	if !blocked {
		t.Fatalf("expected blocked=true within blackout window")
	}
	if w == nil {
		t.Fatalf("expected matched window, got nil")
	}
	if w.Mode != ModeBlackout {
		t.Fatalf("want mode=blackout, got %q", w.Mode)
	}
}

func TestEvaluator_BlackoutOutsideWindow_NotBlocked(t *testing.T) {
	// Saturday 14:00 UTC — outside Mon-Fri.
	now := time.Date(2026, 5, 16, 14, 0, 0, 0, time.UTC)
	ev := &fixedEvaluator{windows: []Window{testWindow()}}
	blocked, w, err := IsBlocked(context.Background(), ev, OpClusterDelete, nil, now)
	if err != nil {
		t.Fatalf("IsBlocked: %v", err)
	}
	if blocked {
		t.Fatalf("expected blocked=false outside blackout window, matched %v", w)
	}
}

func TestEvaluator_PermittedOutsideWindow_Blocked(t *testing.T) {
	// Saturday outside the permitted window → permitted-mode refuses.
	now := time.Date(2026, 5, 16, 14, 0, 0, 0, time.UTC)
	w := testWindow(func(w *Window) { w.Mode = ModePermitted })
	ev := &fixedEvaluator{windows: []Window{w}}
	blocked, matched, err := IsBlocked(context.Background(), ev, OpClusterDelete, nil, now)
	if err != nil {
		t.Fatalf("IsBlocked: %v", err)
	}
	if !blocked {
		t.Fatalf("expected blocked=true outside permitted window")
	}
	if matched == nil || matched.Mode != ModePermitted {
		t.Fatalf("expected matched permitted window, got %v", matched)
	}
}

func TestEvaluator_PermittedInsideWindow_NotBlocked(t *testing.T) {
	now := time.Date(2026, 5, 12, 14, 0, 0, 0, time.UTC) // Tuesday 14:00
	w := testWindow(func(w *Window) { w.Mode = ModePermitted })
	ev := &fixedEvaluator{windows: []Window{w}}
	blocked, _, err := IsBlocked(context.Background(), ev, OpClusterDelete, nil, now)
	if err != nil {
		t.Fatalf("IsBlocked: %v", err)
	}
	if blocked {
		t.Fatalf("expected blocked=false inside permitted window")
	}
}

func TestEvaluator_ClusterSelectorScope(t *testing.T) {
	now := time.Date(2026, 5, 12, 14, 0, 0, 0, time.UTC)
	w := testWindow(func(w *Window) {
		w.ClusterSelector = map[string]string{"tier": "prod"}
	})
	ev := &fixedEvaluator{windows: []Window{w}}

	// tier=prod cluster → blocked.
	blocked, _, err := IsBlocked(context.Background(), ev, OpClusterDelete, map[string]string{"tier": "prod"}, now)
	if err != nil {
		t.Fatalf("IsBlocked: %v", err)
	}
	if !blocked {
		t.Fatalf("expected tier=prod cluster to be blocked by tier=prod window")
	}

	// tier=dev cluster → not blocked.
	blocked, _, err = IsBlocked(context.Background(), ev, OpClusterDelete, map[string]string{"tier": "dev"}, now)
	if err != nil {
		t.Fatalf("IsBlocked: %v", err)
	}
	if blocked {
		t.Fatalf("expected tier=dev cluster to NOT be blocked by tier=prod window")
	}

	// Unlabeled cluster → not blocked.
	blocked, _, err = IsBlocked(context.Background(), ev, OpClusterDelete, nil, now)
	if err != nil {
		t.Fatalf("IsBlocked: %v", err)
	}
	if blocked {
		t.Fatalf("expected unlabeled cluster to NOT be blocked by tier=prod window")
	}
}

func TestEvaluator_OperationTypeFilter(t *testing.T) {
	now := time.Date(2026, 5, 12, 14, 0, 0, 0, time.UTC)
	w := testWindow(func(w *Window) {
		w.OperationTypes = []string{OpClusterDelete}
	})
	ev := &fixedEvaluator{windows: []Window{w}}

	// cluster.delete → blocked.
	blocked, _, err := IsBlocked(context.Background(), ev, OpClusterDelete, nil, now)
	if err != nil {
		t.Fatalf("IsBlocked: %v", err)
	}
	if !blocked {
		t.Fatalf("expected cluster.delete blocked when window scopes to it")
	}

	// tool.upgrade → not blocked (out of scope).
	blocked, _, err = IsBlocked(context.Background(), ev, OpToolUpgrade, nil, now)
	if err != nil {
		t.Fatalf("IsBlocked: %v", err)
	}
	if blocked {
		t.Fatalf("expected tool.upgrade NOT blocked when window scopes to cluster.delete only")
	}
}

func TestEvaluator_TimezoneAware(t *testing.T) {
	// Window: 9am Mon-Fri in America/New_York (= 14:00 UTC during EDT in May).
	w := testWindow(func(w *Window) { w.Timezone = "America/New_York" })

	// 14:00 UTC on Tuesday 2026-05-12 == 10:00 EDT → inside the 9am-5pm window.
	now := time.Date(2026, 5, 12, 14, 0, 0, 0, time.UTC)
	ev := &fixedEvaluator{windows: []Window{w}}
	blocked, _, err := IsBlocked(context.Background(), ev, OpClusterDelete, nil, now)
	if err != nil {
		t.Fatalf("IsBlocked: %v", err)
	}
	if !blocked {
		t.Fatalf("expected blocked=true at 10:00 EDT on Tuesday with NY-timezone window")
	}

	// 04:00 UTC on Tuesday 2026-05-12 == 00:00 EDT → outside window.
	now = time.Date(2026, 5, 12, 4, 0, 0, 0, time.UTC)
	blocked, _, err = IsBlocked(context.Background(), ev, OpClusterDelete, nil, now)
	if err != nil {
		t.Fatalf("IsBlocked: %v", err)
	}
	if blocked {
		t.Fatalf("expected NOT blocked at 00:00 EDT on Tuesday")
	}
}

func TestNextOpen_ComputesCorrectly(t *testing.T) {
	// Saturday 14:00 UTC: next 9am Mon-Fri = Monday 9am UTC.
	now := time.Date(2026, 5, 16, 14, 0, 0, 0, time.UTC)
	w := testWindow()
	next := NextOpen(w, now)
	want := time.Date(2026, 5, 18, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("NextOpen: got %v want %v", next, want)
	}
}

func TestNextClose_Active(t *testing.T) {
	now := time.Date(2026, 5, 12, 14, 0, 0, 0, time.UTC) // Tuesday 14:00
	w := testWindow()                                     // 8h window starting 9am
	close := NextClose(w, now)
	want := time.Date(2026, 5, 12, 17, 0, 0, 0, time.UTC)
	if !close.Equal(want) {
		t.Fatalf("NextClose: got %v want %v", close, want)
	}
}

func TestEvaluator_DisabledWindowIgnored(t *testing.T) {
	now := time.Date(2026, 5, 12, 14, 0, 0, 0, time.UTC)
	w := testWindow(func(w *Window) { w.Enabled = false })
	ev := &fixedEvaluator{windows: []Window{w}}
	blocked, _, err := IsBlocked(context.Background(), ev, OpClusterDelete, nil, now)
	if err != nil {
		t.Fatalf("IsBlocked: %v", err)
	}
	if blocked {
		t.Fatalf("expected disabled window to be ignored")
	}
}

func TestEvaluator_BlackoutPrefersOverPermitted(t *testing.T) {
	// Two windows: a blackout that matches and a permitted that also
	// matches. Result should be a blackout-mode block.
	now := time.Date(2026, 5, 12, 14, 0, 0, 0, time.UTC)
	black := testWindow(func(w *Window) { w.Name = "black" })
	perm := testWindow(func(w *Window) {
		w.Name = "perm"
		w.Mode = ModePermitted
	})
	// perm is currently inside its permitted window so it wouldn't
	// block by itself; the blackout one should win.
	ev := &fixedEvaluator{windows: []Window{perm, black}}
	blocked, matched, err := IsBlocked(context.Background(), ev, OpClusterDelete, nil, now)
	if err != nil {
		t.Fatalf("IsBlocked: %v", err)
	}
	if !blocked {
		t.Fatalf("expected blocked=true")
	}
	if matched.Mode != ModeBlackout {
		t.Fatalf("expected blackout to be picked over permitted, got %q", matched.Mode)
	}
}

func TestEvaluator_NilSafe(t *testing.T) {
	blocked, _, err := IsBlocked(context.Background(), nil, OpClusterDelete, nil, time.Now())
	if err != nil {
		t.Fatalf("IsBlocked with nil evaluator: %v", err)
	}
	if blocked {
		t.Fatalf("expected nil evaluator to short-circuit to not-blocked")
	}
}

// TestEvaluator_CachesAcrossCalls verifies the Evaluator caches the
// underlying query result for at least one re-read inside the TTL.
func TestEvaluator_CachesAcrossCalls(t *testing.T) {
	q := &countingQuerier{}
	ev := NewEvaluator(q)
	if _, err := ev.Windows(context.Background()); err != nil {
		t.Fatalf("Windows: %v", err)
	}
	if _, err := ev.Windows(context.Background()); err != nil {
		t.Fatalf("Windows (cached): %v", err)
	}
	if q.calls != 1 {
		t.Fatalf("expected 1 DB call (second served from cache), got %d", q.calls)
	}
}

func TestEvaluator_InvalidateForcesReread(t *testing.T) {
	q := &countingQuerier{}
	ev := NewEvaluator(q)
	if _, err := ev.Windows(context.Background()); err != nil {
		t.Fatalf("Windows: %v", err)
	}
	ev.Invalidate()
	if _, err := ev.Windows(context.Background()); err != nil {
		t.Fatalf("Windows: %v", err)
	}
	if q.calls != 2 {
		t.Fatalf("expected 2 DB calls after invalidate, got %d", q.calls)
	}
}
