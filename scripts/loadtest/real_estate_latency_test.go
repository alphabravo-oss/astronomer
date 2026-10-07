package main

import (
	"testing"
	"time"
)

func TestRealEstateLatencyIncludesSlowTailBeyondFormerCap(t *testing.T) {
	rec := newEstateRequestRecorder([]estateRequest{{Scenario: "test"}})
	for i := 0; i < 50000; i++ {
		rec.scheduled(0)
		rec.completed(0, time.Millisecond, time.Millisecond, "")
	}
	for i := 0; i < 1000; i++ {
		rec.scheduled(0)
		rec.completed(0, 600*time.Millisecond, 3*time.Second, "")
	}
	r := rec.results()[0]
	if r.Success != 51000 || r.HeaderLatency.Samples != 51000 || r.FullResponseLatency.Samples != 51000 {
		t.Fatal("late observations omitted")
	}
	if r.HeaderLatency.P99UpperBoundMS == nil || *r.HeaderLatency.P99UpperBoundMS != 1000 || r.FullResponseLatency.P99UpperBoundMS == nil || *r.FullResponseLatency.P99UpperBoundMS != 5000 {
		t.Fatalf("slow tail hidden: %+v", r)
	}
	var total uint64
	for _, n := range r.HeaderLatency.Counts {
		total += n
	}
	if total+r.HeaderLatency.Overflow != 51000 {
		t.Fatal("histogram conservation failed")
	}
}
func TestRealEstateLatencyBudgetBoundariesAndOverflow(t *testing.T) {
	for _, elapsed := range []time.Duration{500 * time.Millisecond, 2 * time.Second, 61 * time.Second} {
		var h estateLatencyHistogram
		h.observe(elapsed)
		r := h.distribution()
		if elapsed > time.Minute {
			if r.Overflow != 1 || r.P99UpperBoundMS != nil {
				t.Fatal("overflow clamped to finite p99")
			}
		} else if r.P99UpperBoundMS == nil || *r.P99UpperBoundMS != float64(elapsed)/float64(time.Millisecond) {
			t.Fatal("budget boundary rounded incorrectly")
		}
	}
	if (estateLatencyHistogram{}).distribution().P99UpperBoundMS != nil {
		t.Fatal("empty histogram manufactured p99")
	}
}
