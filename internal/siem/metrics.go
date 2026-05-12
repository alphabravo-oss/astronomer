package siem

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// dispatchedTotal is the per-forwarder count of events the dispatcher
// shipped. The format label lets operators correlate a sudden NDJSON
// drop with a Splunk receiver rejection without slicing by transport
// name. The outcome label is "delivered" | "failed" — same vocabulary
// as the webhook metrics so dashboards can reuse panels.
var dispatchedTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "astronomer",
		Subsystem: "siem",
		Name:      "events_dispatched_total",
		Help:      "Total SIEM forwarder dispatch attempts grouped by forwarder, format, and outcome.",
	},
	observability.MetricLabels("forwarder", "format", "outcome"),
)

// queueDepth is the live per-forwarder queue size. The bus tap polls
// this on every enqueue to know whether to drop oldest rows; the
// dispatcher updates it on every tick after the batch ships.
var queueDepth = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "astronomer",
		Subsystem: "siem",
		Name:      "queue_depth",
		Help:      "Current number of queued events awaiting dispatch per forwarder.",
	},
	observability.MetricLabels("forwarder"),
)

// droppedTotal is the per-forwarder count of events the platform dropped
// before they reached the SIEM. Reason labels:
//   - "queue_full" — bounded queue cap hit; oldest rows evicted.
//   - "retries_exhausted" — row crossed the 100-attempt retry budget.
//   - "format_error" — formatter returned no bytes (defensive — shouldn't happen).
var droppedTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "astronomer",
		Subsystem: "siem",
		Name:      "dropped_total",
		Help:      "Total SIEM events dropped before reaching the sink, grouped by forwarder and reason.",
	},
	observability.MetricLabels("forwarder", "reason"),
)

func init() {
	prometheus.MustRegister(dispatchedTotal)
	prometheus.MustRegister(queueDepth)
	prometheus.MustRegister(droppedTotal)
}

// RecordDispatched bumps the per-forwarder dispatched counter. Called
// by the worker dispatcher on every (success | failure) outcome.
func RecordDispatched(forwarder, format, outcome string, n int) {
	if forwarder == "" {
		forwarder = "unknown"
	}
	if format == "" {
		format = "rfc5424"
	}
	if outcome == "" {
		outcome = "unknown"
	}
	dispatchedTotal.
		WithLabelValues(observability.MetricValues(forwarder, format, outcome)...).
		Add(float64(n))
}

// RecordQueueDepth sets the per-forwarder gauge. The dispatcher calls
// this after each tick using the live row count.
func RecordQueueDepth(forwarder string, depth int) {
	if forwarder == "" {
		forwarder = "unknown"
	}
	queueDepth.
		WithLabelValues(observability.MetricValues(forwarder)...).
		Set(float64(depth))
}

// RecordDropped bumps the dropped counter for a reason. Called by the
// bus tap (queue_full) and the dispatcher (retries_exhausted /
// format_error).
func RecordDropped(forwarder, reason string, n int) {
	if forwarder == "" {
		forwarder = "unknown"
	}
	if reason == "" {
		reason = "unknown"
	}
	droppedTotal.
		WithLabelValues(observability.MetricValues(forwarder, reason)...).
		Add(float64(n))
}
