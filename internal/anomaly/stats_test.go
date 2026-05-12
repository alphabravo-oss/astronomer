package anomaly

import (
	"math"
	"testing"
	"time"
)

func TestCompute_HandlesEmptySliceSafely(t *testing.T) {
	got := Compute(nil)
	if got.Count != 0 {
		t.Fatalf("empty input must have Count=0, got %d", got.Count)
	}
	if got.Mean != 0 || got.Stddev != 0 || got.P50 != 0 || got.P95 != 0 || got.P99 != 0 {
		t.Fatalf("empty input must produce zero stats, got %+v", got)
	}
	// Also explicitly check the IsAnomaly cold-start gate using
	// an empty-stats Stats — it must not fire.
	if IsAnomaly(got, 9999, 3.0, "above", 50) {
		t.Fatalf("IsAnomaly must short-circuit when stats.Count < minSamples")
	}
}

// samplesOneToTen returns 1..10 with a strictly increasing
// timestamp so LastValue is deterministic.
func samplesOneToTen() []Sample {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	out := make([]Sample, 0, 10)
	for i := 1; i <= 10; i++ {
		out = append(out, Sample{
			Value: float64(i),
			Time:  base.Add(time.Duration(i) * time.Minute),
		})
	}
	return out
}

// Golden values cross-checked with numpy on the input 1..10:
//
//	>>> import numpy as np
//	>>> x = np.arange(1, 11, dtype=float)
//	>>> x.mean()        -> 5.5
//	>>> x.std()         -> 2.8722813232690143  (population stddev)
//	>>> np.percentile(x, 50) -> 5.5
//	>>> np.percentile(x, 95) -> 9.55
//	>>> np.percentile(x, 99) -> 9.91
func TestCompute_MeanMatchesNumpy(t *testing.T) {
	got := Compute(samplesOneToTen())
	if math.Abs(got.Mean-5.5) > 1e-9 {
		t.Fatalf("mean: want 5.5, got %v", got.Mean)
	}
}

func TestCompute_StddevMatchesNumpy(t *testing.T) {
	got := Compute(samplesOneToTen())
	want := 2.8722813232690143
	if math.Abs(got.Stddev-want) > 1e-9 {
		t.Fatalf("stddev: want %v, got %v", want, got.Stddev)
	}
}

func TestCompute_P95MatchesNumpy(t *testing.T) {
	got := Compute(samplesOneToTen())
	if math.Abs(got.P50-5.5) > 1e-9 {
		t.Fatalf("p50: want 5.5, got %v", got.P50)
	}
	if math.Abs(got.P95-9.55) > 1e-9 {
		t.Fatalf("p95: want 9.55, got %v", got.P95)
	}
	if math.Abs(got.P99-9.91) > 1e-9 {
		t.Fatalf("p99: want 9.91, got %v", got.P99)
	}
}

func TestCompute_MinMaxLastValue(t *testing.T) {
	got := Compute(samplesOneToTen())
	if got.Min != 1 || got.Max != 10 {
		t.Fatalf("min/max: want 1/10, got %v/%v", got.Min, got.Max)
	}
	if got.LastValue != 10 {
		t.Fatalf("last_value: want 10, got %v", got.LastValue)
	}
}

func TestCompute_IgnoresNonFinite(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	in := []Sample{
		{Value: math.NaN(), Time: base.Add(1 * time.Minute)},
		{Value: math.Inf(1), Time: base.Add(2 * time.Minute)},
		{Value: 5.0, Time: base.Add(3 * time.Minute)},
		{Value: 5.0, Time: base.Add(4 * time.Minute)},
	}
	got := Compute(in)
	if got.Count != 2 {
		t.Fatalf("non-finite samples must be skipped, got Count=%d", got.Count)
	}
	if got.Mean != 5 || got.Stddev != 0 {
		t.Fatalf("mean/stddev after non-finite skip: want 5/0, got %v/%v", got.Mean, got.Stddev)
	}
}

func TestIsAnomaly_AboveModeFires(t *testing.T) {
	// stats with mean=10, stddev=1, count high enough to pass gate.
	st := Stats{Count: 100, Mean: 10, Stddev: 1}
	if !IsAnomaly(st, 13.5, 3.0, "above", 50) {
		t.Fatalf("13.5 > 10 + 3*1=13 must fire above")
	}
	if IsAnomaly(st, 12.9, 3.0, "above", 50) {
		t.Fatalf("12.9 is within threshold, must not fire above")
	}
	// "below" excursion must not fire in "above" mode.
	if IsAnomaly(st, 6, 3.0, "above", 50) {
		t.Fatalf("low value must not fire in above mode")
	}
}

func TestIsAnomaly_BelowModeFires(t *testing.T) {
	st := Stats{Count: 100, Mean: 10, Stddev: 1}
	if !IsAnomaly(st, 6.5, 3.0, "below", 50) {
		t.Fatalf("6.5 < 10 - 3*1=7 must fire below")
	}
	if IsAnomaly(st, 7.1, 3.0, "below", 50) {
		t.Fatalf("7.1 is within threshold, must not fire below")
	}
	if IsAnomaly(st, 14, 3.0, "below", 50) {
		t.Fatalf("high value must not fire in below mode")
	}
}

func TestIsAnomaly_EitherModeFires(t *testing.T) {
	st := Stats{Count: 100, Mean: 10, Stddev: 1}
	if !IsAnomaly(st, 13.5, 3.0, "either", 50) {
		t.Fatalf("high excursion must fire in either mode")
	}
	if !IsAnomaly(st, 6.5, 3.0, "either", 50) {
		t.Fatalf("low excursion must fire in either mode")
	}
	if IsAnomaly(st, 11.5, 3.0, "either", 50) {
		t.Fatalf("within-threshold deviation must not fire")
	}
}

func TestIsAnomaly_RespectsMinSamplesGate(t *testing.T) {
	// Mean=0 and stddev=0 is the classic "freshly created rule"
	// state — without the gate, ANY non-zero value would
	// falsely fire. The gate must short-circuit before any of
	// the direction logic runs.
	st := Stats{Count: 10, Mean: 0, Stddev: 0}
	if IsAnomaly(st, 100, 3.0, "above", 50) {
		t.Fatalf("must not fire when Count < minSamples (cold-start guard)")
	}
	// And once enough samples are in, but stddev is still zero
	// (perfectly flat baseline), we still must not fire — that's
	// the "monitoring noise at the floor of measurement"
	// degenerate case.
	st = Stats{Count: 100, Mean: 5, Stddev: 0}
	if IsAnomaly(st, 5.5, 3.0, "above", 50) {
		t.Fatalf("must not fire when stddev=0 (flat baseline)")
	}
}

func TestIsAnomaly_RejectsBadInputs(t *testing.T) {
	st := Stats{Count: 100, Mean: 10, Stddev: 1}
	if IsAnomaly(st, math.NaN(), 3.0, "above", 50) {
		t.Fatalf("NaN current must not fire")
	}
	if IsAnomaly(st, 100, 0, "above", 50) {
		t.Fatalf("stddevMult=0 must not fire")
	}
	if IsAnomaly(st, 100, -1, "above", 50) {
		t.Fatalf("negative stddevMult must not fire")
	}
}
