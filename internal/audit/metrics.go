package audit

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// auditDroppedTotal counts audit events that were dropped by the async batched
// writer because the in-process buffer channel was full. Under sustained
// overload we prefer dropping audit events to back-pressuring the HTTP
// handler — the metric (and the throttled log at every 1000th drop) is the
// operator's signal that the buffer is undersized or the database is too slow
// to keep up with the audit-event rate.
var auditDroppedTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "astronomer",
		Subsystem: "audit",
		Name:      "dropped_total",
		Help:      "Total number of audit events dropped because the async writer buffer was full.",
	},
	observability.MetricLabels("reason"),
)

// auditBatchInsertsTotal counts successful batch insert operations. Useful
// for confirming the batching path is actually exercised in production.
var auditBatchInsertsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "astronomer",
		Subsystem: "audit",
		Name:      "batch_inserts_total",
		Help:      "Total number of audit-log batch insert operations performed by the async writer.",
	},
	observability.MetricLabels("outcome"),
)

// auditBatchSize observes the number of rows in each batch insert. The
// distribution makes it easy to spot the "always tiny" case (batching not
// helping) and the "always max" case (buffer is saturated).
var auditBatchSize = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: "astronomer",
		Subsystem: "audit",
		Name:      "batch_size",
		Help:      "Distribution of audit-log batch sizes flushed by the async writer.",
		Buckets:   []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000},
	},
	observability.MetricLabels(),
)

func init() {
	prometheus.MustRegister(auditDroppedTotal)
	prometheus.MustRegister(auditBatchInsertsTotal)
	prometheus.MustRegister(auditBatchSize)
}

// recordDropped increments the drop counter for the given reason.
func recordDropped(reason string) {
	if reason == "" {
		reason = "buffer_full"
	}
	auditDroppedTotal.WithLabelValues(observability.MetricValues(reason)...).Inc()
}

// recordBatchInsert increments the batch insert counter for the given
// outcome ("ok" or "error") and observes the batch size.
func recordBatchInsert(outcome string, size int) {
	if outcome == "" {
		outcome = "ok"
	}
	auditBatchInsertsTotal.WithLabelValues(observability.MetricValues(outcome)...).Inc()
	auditBatchSize.WithLabelValues(observability.MetricValues()...).Observe(float64(size))
}
