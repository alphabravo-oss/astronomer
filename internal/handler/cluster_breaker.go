// Package handler — per-cluster circuit breaker.
//
// When one cluster's tunnel is wedged, every
// k8s-passthrough request to it burns its full ctx timeout (typically
// 5-30s) and holds a goroutine + WS stream for the duration. With N
// callers and one stuck cluster, the request-side throughput collapses
// even though the OTHER N-1 clusters are healthy.
//
// The breaker fast-fails repeat offenders. State per cluster:
//
//   closed   — normal pass-through. Each consecutive failure increments
//              a counter; on threshold, transition to OPEN.
//   open     — every Do returns `circuit_open` immediately without
//              touching the tunnel. After openDuration, transition to
//              HALF_OPEN.
//   half-open — exactly one trial request is allowed. Success → CLOSED
//              and counter reset. Failure → OPEN with the timer reset.
//
// The breaker is a thin wrapper; callers that already short-circuit on
// "agent not connected" still do so — the breaker only kicks in for
// the "tunnel is up but every call is timing out" failure mode.
package handler

import (
	"errors"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// ErrCircuitOpen is the sentinel error a fast-failed request returns.
// Callers can errors.Is(err, ErrCircuitOpen) to distinguish breaker
// rejections from real cluster errors when deciding whether to retry
// or surface to the user.
var ErrCircuitOpen = errors.New("cluster circuit breaker open")

type breakerState int32

const (
	breakerClosed   breakerState = 0
	breakerOpen     breakerState = 1
	breakerHalfOpen breakerState = 2
)

func (s breakerState) String() string {
	switch s {
	case breakerClosed:
		return "closed"
	case breakerOpen:
		return "open"
	case breakerHalfOpen:
		return "half-open"
	}
	return "unknown"
}

// breakerEntry is the per-cluster state. Each is roughly 64 bytes and
// the map is bounded by the number of clusters we ever observe — well
// inside acceptable for any realistic fleet.
type breakerEntry struct {
	mu           sync.Mutex
	state        breakerState
	consecutiveErrs int
	openedAt     time.Time
}

// clusterBreaker is the shared coordinator. One per *TunnelK8sRequester.
type clusterBreaker struct {
	mu            sync.RWMutex
	entries       map[string]*breakerEntry
	threshold     int           // consecutive failures to OPEN
	openDuration  time.Duration // time before HALF_OPEN
}

// circuitStateGauge surfaces per-cluster state as a metric (0 closed,
// 1 open, 2 half-open). Wired once via init().
var circuitStateGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "astronomer",
		Subsystem: "tunnel",
		Name:      "circuit_state",
		Help:      "Per-cluster tunnel circuit breaker state. 0=closed (healthy), 1=open (failing), 2=half-open (trial).",
	},
	observability.MetricLabels("cluster_id"),
)

// circuitTripsTotal counts how many times each cluster's breaker has
// opened. A high rate over a window is the alertable signal — "this
// cluster keeps flapping" rather than "this cluster is permanently
// degraded".
var circuitTripsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "astronomer",
		Subsystem: "tunnel",
		Name:      "circuit_trips_total",
		Help:      "Total circuit-breaker open transitions per cluster.",
	},
	observability.MetricLabels("cluster_id"),
)

var circuitMetricsRegistered sync.Once

func registerCircuitMetrics() {
	circuitMetricsRegistered.Do(func() {
		prometheus.MustRegister(circuitStateGauge, circuitTripsTotal)
	})
}

func newClusterBreaker(threshold int, openDuration time.Duration) *clusterBreaker {
	if threshold <= 0 {
		threshold = 5
	}
	if openDuration <= 0 {
		openDuration = 30 * time.Second
	}
	registerCircuitMetrics()
	return &clusterBreaker{
		entries:      make(map[string]*breakerEntry),
		threshold:    threshold,
		openDuration: openDuration,
	}
}

func (b *clusterBreaker) entry(clusterID string) *breakerEntry {
	b.mu.RLock()
	e := b.entries[clusterID]
	b.mu.RUnlock()
	if e != nil {
		return e
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if e = b.entries[clusterID]; e != nil {
		return e
	}
	e = &breakerEntry{}
	b.entries[clusterID] = e
	return e
}

// allow checks the breaker state and returns whether the caller should
// proceed. If proceeding, the caller MUST follow up with recordSuccess
// or recordFailure to close the loop. The third return value is a
// finalize callback that records the outcome; callers should defer it
// and pass nil for success or the error for failure.
func (b *clusterBreaker) allow(clusterID string) (proceed bool, finalize func(err error)) {
	e := b.entry(clusterID)
	e.mu.Lock()
	defer e.mu.Unlock()

	switch e.state {
	case breakerOpen:
		// Has the cooldown elapsed?
		if time.Since(e.openedAt) >= b.openDuration {
			// Transition to half-open. One trial request gets through.
			e.state = breakerHalfOpen
			circuitStateGauge.WithLabelValues(observability.MetricValues(clusterID)...).Set(float64(breakerHalfOpen))
			return true, func(err error) { b.finalize(clusterID, e, err) }
		}
		// Still open: fail fast.
		return false, func(error) {} // no-op finalize
	case breakerHalfOpen:
		// Only allow ONE trial. We're already past `allow` once; the
		// caller is using up the trial slot. (We don't gate concurrent
		// callers because the breaker is per-cluster and concurrent
		// requests are rare during a recovery window; if multiple slip
		// through they all converge on the same outcome.)
		return true, func(err error) { b.finalize(clusterID, e, err) }
	default: // closed
		return true, func(err error) { b.finalize(clusterID, e, err) }
	}
}

func (b *clusterBreaker) finalize(clusterID string, e *breakerEntry, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err == nil {
		// Success: reset failure counter, force CLOSED.
		if e.state != breakerClosed {
			e.state = breakerClosed
			circuitStateGauge.WithLabelValues(observability.MetricValues(clusterID)...).Set(float64(breakerClosed))
		}
		e.consecutiveErrs = 0
		return
	}
	// Failure path.
	e.consecutiveErrs++
	switch e.state {
	case breakerClosed:
		if e.consecutiveErrs >= b.threshold {
			e.state = breakerOpen
			e.openedAt = time.Now()
			circuitStateGauge.WithLabelValues(observability.MetricValues(clusterID)...).Set(float64(breakerOpen))
			circuitTripsTotal.WithLabelValues(observability.MetricValues(clusterID)...).Inc()
		}
	case breakerHalfOpen:
		// Trial failed — re-open with the timer reset.
		e.state = breakerOpen
		e.openedAt = time.Now()
		circuitStateGauge.WithLabelValues(observability.MetricValues(clusterID)...).Set(float64(breakerOpen))
		circuitTripsTotal.WithLabelValues(observability.MetricValues(clusterID)...).Inc()
	}
}

// state is a test/diagnostic accessor; not used by hot paths.
func (b *clusterBreaker) state(clusterID string) breakerState {
	b.mu.RLock()
	e := b.entries[clusterID]
	b.mu.RUnlock()
	if e == nil {
		return breakerClosed
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}
