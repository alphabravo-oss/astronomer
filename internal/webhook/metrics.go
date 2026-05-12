package webhook

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// deliveryOutcomes counts per-outcome attempts: delivered (2xx),
// failed (5xx / transport — will retry), dropped (4xx / retry budget
// exhausted / oversized payload). The label is the same one stamped on
// the webhook_deliveries.status column so an operator can correlate
// metrics with rows.
var deliveryOutcomes = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "astronomer",
		Subsystem: "webhook",
		Name:      "deliveries_total",
		Help:      "Total webhook delivery attempts grouped by outcome (delivered/failed/dropped).",
	},
	observability.MetricLabels("outcome"),
)

// deliveryDuration captures end-to-end POST latency (DNS + TCP + TLS +
// body upload + server processing). Useful for spotting a slow receiver
// before the timeout fires.
var deliveryDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: "astronomer",
		Subsystem: "webhook",
		Name:      "delivery_duration_seconds",
		Help:      "Wall-clock duration of webhook delivery POST attempts.",
		Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	},
	observability.MetricLabels("outcome"),
)

func init() {
	prometheus.MustRegister(deliveryOutcomes)
	prometheus.MustRegister(deliveryDuration)
}

// RecordOutcome bumps the per-outcome counter + observes the duration.
// Called by the dispatcher after every Send.
func RecordOutcome(outcome string, durationSeconds float64) {
	if outcome == "" {
		outcome = "unknown"
	}
	deliveryOutcomes.WithLabelValues(observability.MetricValues(outcome)...).Inc()
	deliveryDuration.WithLabelValues(observability.MetricValues(outcome)...).Observe(durationSeconds)
}
