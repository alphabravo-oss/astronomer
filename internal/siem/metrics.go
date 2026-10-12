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
// A name-only caller cannot prove current ownership. Legacy public helpers
// retain finite unknown aggregate counters; production async consumers use a
// generation-bound MetricLease. The bus hot path stays free of database reads.
func RecordDispatched(_ string, format, outcome string, n int) {
	if n <= 0 {
		return
	}
	dispatchedTotal.WithLabelValues(observability.MetricValues("unknown", formatLabel(format), outcomeLabel(outcome))...).Add(float64(n))
}
func RecordQueueDepth(_ string, _ int) {
	// Unknown names cannot publish a current per-owner gauge. Product queues are
	// unaffected; the qualified dispatcher uses MetricLease.QueueDepth instead.
}
func RecordDropped(_ string, reason string, n int) {
	if n <= 0 {
		return
	}
	droppedTotal.WithLabelValues(observability.MetricValues("unknown", reasonLabel(reason))...).Add(float64(n))
}
