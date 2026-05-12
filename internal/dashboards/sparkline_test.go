package dashboards

import (
	"strings"
	"testing"
)

// TestRenderSparkline_Empty — zero or one sample → empty SVG body (no
// polyline). The handler relies on this so degenerate Prom query
// responses still produce a well-formed SVG that won't break the
// browser's parser.
func TestRenderSparkline_Empty(t *testing.T) {
	out := string(RenderSparkline(PromMatrix{}))
	if !strings.HasPrefix(out, "<svg") {
		t.Fatalf("expected SVG envelope, got %q", out)
	}
	if !strings.HasSuffix(out, "</svg>") {
		t.Fatalf("expected closing tag, got %q", out)
	}
	if strings.Contains(out, "<polyline") {
		t.Fatalf("empty matrix must not emit polyline, got %q", out)
	}

	one := string(RenderSparkline(PromMatrix{Samples: []PromSample{{Time: 1, Value: 5}}}))
	if strings.Contains(one, "<polyline") {
		t.Fatalf("single-sample matrix must not emit polyline, got %q", one)
	}
}

// TestRenderSparkline_WithMatrix — multi-sample input must produce a
// polyline with the right number of points + min/max ticks. We don't
// pixel-test the coords; we just verify the structural invariants the
// handler depends on.
func TestRenderSparkline_WithMatrix(t *testing.T) {
	m := PromMatrix{Samples: []PromSample{
		{Time: 0, Value: 1},
		{Time: 1, Value: 3},
		{Time: 2, Value: 2},
		{Time: 3, Value: 5},
		{Time: 4, Value: 4},
	}}
	out := string(RenderSparkline(m))
	if !strings.Contains(out, "<polyline") {
		t.Fatalf("multi-sample matrix must emit polyline; got %q", out)
	}
	// Two tick segments (min + max).
	if got := strings.Count(out, "<line "); got != 2 {
		t.Fatalf("expected 2 tick <line> markers, got %d in %q", got, out)
	}
	// All-equal samples → flat midline (single <line>).
	flat := string(RenderSparkline(PromMatrix{Samples: []PromSample{
		{Time: 0, Value: 7}, {Time: 1, Value: 7}, {Time: 2, Value: 7},
	}}))
	if strings.Contains(flat, "<polyline") {
		t.Fatalf("all-equal samples should not emit polyline; got %q", flat)
	}
	if got := strings.Count(flat, "<line "); got != 1 {
		t.Fatalf("expected 1 baseline <line>, got %d in %q", got, flat)
	}
}
