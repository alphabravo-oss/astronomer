package main

import "time"

// Non-cumulative fixed buckets cover every completion without retaining raw
// samples. Reported p99 is the containing bucket's conservative upper bound;
// a percentile in overflow is unavailable, never clamped to the last boundary.
var estateLatencyBoundsMS = [...]float64{1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 15000, 30000, 60000}

type estateLatencyHistogram struct {
	counts          [len(estateLatencyBoundsMS)]uint64
	overflow, total uint64
}
type estateLatencyDistribution struct {
	UpperBoundsMS   []float64 `json:"bucket_upper_bounds_ms"`
	Counts          []uint64  `json:"noncumulative_bucket_counts"`
	Samples         uint64    `json:"samples"`
	Overflow        uint64    `json:"overflow"`
	P99UpperBoundMS *float64  `json:"p99_upper_bound_ms"`
}

func (h *estateLatencyHistogram) observe(elapsed time.Duration) {
	h.total++
	ms := float64(elapsed) / float64(time.Millisecond)
	for i, bound := range estateLatencyBoundsMS {
		if ms <= bound {
			h.counts[i]++
			return
		}
	}
	h.overflow++
}
func (h estateLatencyHistogram) distribution() estateLatencyDistribution {
	r := estateLatencyDistribution{UpperBoundsMS: append([]float64(nil), estateLatencyBoundsMS[:]...), Counts: append([]uint64(nil), h.counts[:]...), Samples: h.total, Overflow: h.overflow}
	target := (h.total*99 + 99) / 100
	if target == 0 {
		return r
	}
	var count uint64
	for i, n := range h.counts {
		count += n
		if count >= target {
			upper := estateLatencyBoundsMS[i]
			r.P99UpperBoundMS = &upper
			break
		}
	}
	return r
}
