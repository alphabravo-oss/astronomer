// Package observability — reconciler instrumentation.
//
// FEATURES-051126 T16: every periodic task that flows through
// internal/worker/tasks.runPeriodicTaskWithLeader gets first-class
// metrics for free:
//
//   - astronomer_reconciler_runs_total{name,status}
//     — counts every run by outcome (succeeded / failed / skipped /
//     errored — the last two are distinct: skipped means the leader
//     election went elsewhere, errored means the lease lookup itself
//     failed).
//   - astronomer_reconciler_last_success_timestamp_seconds{name}
//     — wall-clock seconds of the most recent successful run. Drives
//     the AstronomerReconcilerStalled rule shipped with T03; alerts
//     fire when this lags more than 2× the reconciler's expected
//     interval.
//   - astronomer_reconciler_duration_seconds{name}
//     — histogram of run latency, useful for spotting reconcilers
//     that are slowly creeping toward their timeout.
//
// The metrics live in the observability package rather than the worker
// package so future callers outside the periodic-task path (e.g. an
// in-process reconciler kicked off from the server pod) can use the
// same instrumentation without importing worker.
package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	ReconcilerStatusSucceeded = "succeeded"
	ReconcilerStatusFailed    = "failed"
	ReconcilerStatusSkipped   = "skipped" // leader election picked a different replica
	ReconcilerStatusErrored   = "errored" // could not even attempt — e.g. lease lookup failed
)

var (
	reconcilerRunsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Subsystem: "reconciler",
			Name:      "runs_total",
			Help:      "Total reconciler executions by name and outcome.",
		},
		MetricLabels("name", "status"),
	)

	reconcilerLastSuccessTimestamp = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Subsystem: "reconciler",
			Name:      "last_success_timestamp_seconds",
			Help:      "Unix timestamp of the most recent successful reconciler run by name.",
		},
		MetricLabels("name"),
	)

	reconcilerDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "astronomer",
			Subsystem: "reconciler",
			Name:      "duration_seconds",
			Help:      "Reconciler run duration in seconds by name and outcome.",
			// Buckets sized for the actual range of reconciler runs in
			// this codebase: most complete in <100ms (single SQL query +
			// metric emit), some run for tens of seconds (cluster-wide
			// fan-out), the longest are minutes (catalog_sync). One
			// bucket past the upper expected bound so we can spot
			// outliers without losing precision in the normal range.
			Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 300},
		},
		MetricLabels("name", "status"),
	)
)

func init() {
	prometheus.MustRegister(reconcilerRunsTotal)
	prometheus.MustRegister(reconcilerLastSuccessTimestamp)
	prometheus.MustRegister(reconcilerDurationSeconds)
}

// RecordReconcilerRun emits the three metrics for one reconciler run.
// `start` is the timestamp the run began; the helper computes the
// duration. Caller picks the status from the constants above.
//
// Safe to call from multiple goroutines.
func RecordReconcilerRun(name, status string, start time.Time) {
	if name == "" {
		name = "unknown"
	}
	if status == "" {
		status = ReconcilerStatusErrored
	}
	reconcilerRunsTotal.WithLabelValues(MetricValues(name, status)...).Inc()
	reconcilerDurationSeconds.WithLabelValues(MetricValues(name, status)...).Observe(time.Since(start).Seconds())
	if status == ReconcilerStatusSucceeded {
		reconcilerLastSuccessTimestamp.WithLabelValues(MetricValues(name)...).Set(float64(time.Now().Unix()))
	}
}
