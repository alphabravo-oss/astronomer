// Package anomaly implements rolling-window baseline statistics +
// stddev-based deviation checks for sprint 072's anomaly alert rules.
//
// The package is small on purpose. Compute does one Welford-style
// pass for mean+M2, then a sort-and-pick pass for percentiles. At
// N≤5000 (the recompute cap) sort-based percentiles are simpler and
// cheaper to verify than a streaming quantile structure — we'd take
// O(N log N) at most once every 5 minutes, per (cluster, metric).
//
// IsAnomaly is the alert-time check. The min-samples gate is the
// single bug-prevention layer that keeps a freshly-created rule from
// firing on its very first datapoint (when mean=stddev=0 every
// nonzero value looks infinitely-anomalous).
package anomaly

import (
	"math"
	"sort"
	"time"
)

// Sample is a single (value, timestamp) datum.
type Sample struct {
	Value float64
	Time  time.Time
}

// Stats is the aggregate the baseline-recompute worker writes back
// to the anomaly_baselines row, and the alert evaluator reads on
// every tick.
type Stats struct {
	Count       int
	Mean        float64
	Stddev      float64
	Min         float64
	Max         float64
	P50         float64
	P95         float64
	P99         float64
	LastValue   float64
	LastValueAt time.Time
}

// Compute returns mean / stddev / percentiles / min / max for the
// provided sample slice.
//
// Empty input is safe: returns a zero Stats with Count=0 — the caller
// (the recompute worker) treats this as "not enough data yet" and
// IsAnomaly later short-circuits to no-fire.
//
// Stddev uses the population formula (divide by N, not N-1). At the
// sample-counts we typically see (hundreds to low thousands), the
// difference vs. sample-stddev is within rounding error of the
// stddevMult threshold the operator picks. Population is also what
// numpy.std() returns by default, which keeps the golden-value tests
// straightforward.
func Compute(samples []Sample) Stats {
	if len(samples) == 0 {
		return Stats{}
	}

	// Welford one-pass mean + M2 (sum of squared deviations).
	var (
		n    = 0
		mean = 0.0
		m2   = 0.0
		mn   = math.Inf(1)
		mx   = math.Inf(-1)
	)
	values := make([]float64, 0, len(samples))
	var lastValue float64
	var lastValueAt time.Time
	for _, s := range samples {
		// NaN/Inf samples corrupt mean+stddev. Skip them. We still
		// honor "last value" by ignoring non-finite values entirely
		// — operators don't want their alert to anchor on garbage.
		if math.IsNaN(s.Value) || math.IsInf(s.Value, 0) {
			continue
		}
		n++
		delta := s.Value - mean
		mean += delta / float64(n)
		delta2 := s.Value - mean
		m2 += delta * delta2
		if s.Value < mn {
			mn = s.Value
		}
		if s.Value > mx {
			mx = s.Value
		}
		values = append(values, s.Value)
		if s.Time.After(lastValueAt) {
			lastValue = s.Value
			lastValueAt = s.Time
		}
	}
	if n == 0 {
		return Stats{}
	}
	// Edge case: only one sample. lastValue + lastValueAt may not
	// have been set if the single sample's timestamp is the zero
	// time. Anchor them now to the sole datapoint so callers see a
	// sane LastValue even for cold-start one-sample baselines.
	if lastValueAt.IsZero() {
		lastValue = values[len(values)-1]
		lastValueAt = samples[len(samples)-1].Time
	}
	variance := m2 / float64(n)
	stddev := math.Sqrt(variance)

	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)
	return Stats{
		Count:       n,
		Mean:        mean,
		Stddev:      stddev,
		Min:         mn,
		Max:         mx,
		P50:         percentile(sorted, 0.50),
		P95:         percentile(sorted, 0.95),
		P99:         percentile(sorted, 0.99),
		LastValue:   lastValue,
		LastValueAt: lastValueAt,
	}
}

// IsAnomaly returns true if `current` is far enough from `stats.Mean`
// in the requested direction.
//
//	direction="above"  : current > mean + stddevMult * stddev
//	direction="below"  : current < mean - stddevMult * stddev
//	direction="either" : |current - mean| > stddevMult * stddev
//
// Cold-start guard: when stats.Count < minSamples we always return
// false. This is the single most important behavior in the package
// — without it, a fresh rule with mean=stddev=0 fires on the FIRST
// non-zero sample, which is the most common false-positive cause in
// production stddev-based alerting.
func IsAnomaly(stats Stats, current float64, stddevMult float64, direction string, minSamples int) bool {
	if minSamples < 0 {
		minSamples = 0
	}
	if stats.Count < minSamples {
		return false
	}
	if math.IsNaN(current) || math.IsInf(current, 0) {
		return false
	}
	if stddevMult <= 0 {
		// Treating mult=0 as "every deviation counts" would
		// be ambiguous and noisy. Force a no-fire so the
		// operator notices their misconfiguration via the
		// usual "rule did not fire" investigation path.
		return false
	}
	// A flat baseline (stddev=0) means every sample we've seen
	// matched the mean exactly. We don't want a rule to fire on
	// even a tiny deviation in that case because monitoring noise
	// at the floor of measurement (e.g. an integer counter that
	// only flips 0→1) would saturate the alert pipeline. Treat
	// stddev=0 as "no signal yet" and return false.
	if stats.Stddev == 0 {
		return false
	}
	threshold := stddevMult * stats.Stddev
	switch direction {
	case "above":
		return current > stats.Mean+threshold
	case "below":
		return current < stats.Mean-threshold
	case "either":
		return math.Abs(current-stats.Mean) > threshold
	default:
		// Unknown direction: default to the safer "above" semantics
		// rather than firing on nothing.
		return current > stats.Mean+threshold
	}
}

// percentile returns the linear-interpolation percentile of an
// already-sorted slice. p is in [0,1]. This matches numpy.percentile
// with the default 'linear' interpolation method.
func percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[n-1]
	}
	pos := p * float64(n-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}
