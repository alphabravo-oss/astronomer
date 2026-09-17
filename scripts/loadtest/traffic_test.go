package main

import (
	"testing"
	"time"
)

func TestWorkloadTargetBoundsStartupAndConservesRate(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		rps      int
		maxBatch int
	}{
		{rps: 503, maxBatch: 1},
		{rps: 1500, maxBatch: 2},
		{rps: 5000, maxBatch: 5},
	} {
		ticksPerSecond := minInt(test.rps, workloadTicksPerSecond)
		interval := time.Second / time.Duration(ticksPerSecond)
		maximum := test.rps
		previous := 0
		for tick := 0; tick < ticksPerSecond; tick++ {
			target := workloadTarget(test.rps, time.Duration(tick+1)*interval, maximum)
			batch := target - previous
			if batch > test.maxBatch {
				t.Fatalf("rps %d tick %d batch = %d, want at most %d", test.rps, tick, batch, test.maxBatch)
			}
			previous = target
		}
		if previous != test.rps {
			t.Fatalf("rps %d one-second scheduled total = %d", test.rps, previous)
		}
	}
}

func TestWorkloadTargetRecoversDroppedTicksWithoutExceedingMaximum(t *testing.T) {
	t.Parallel()

	if got := workloadTarget(500, 22*time.Millisecond, 500); got != 11 {
		t.Fatalf("target after a coalesced 22ms wake-up = %d, want 11", got)
	}
	if got := workloadTarget(500, 2*time.Second, 500); got != 500 {
		t.Fatalf("target beyond the window = %d, want capped 500", got)
	}
}
