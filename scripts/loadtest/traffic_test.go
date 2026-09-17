package main

import "testing"

func TestNextWorkloadBatchBoundsStartupAndConservesRate(t *testing.T) {
	t.Parallel()

	credit := 0
	total := 0
	for tick := 0; tick < workloadTicksPerSecond; tick++ {
		batch := nextWorkloadBatch(503, workloadTicksPerSecond, &credit)
		if batch > 6 {
			t.Fatalf("tick %d batch = %d, want at most 6", tick, batch)
		}
		total += batch
	}
	if total != 503 {
		t.Fatalf("one-second scheduled total = %d, want 503", total)
	}
}
