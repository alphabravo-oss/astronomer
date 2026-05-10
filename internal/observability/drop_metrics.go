package observability

import "github.com/prometheus/client_golang/prometheus"

var droppedEventsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "astronomer",
		Name:      "dropped_events_total",
		Help:      "Total number of intentionally dropped events or messages on bounded asynchronous paths.",
	},
	MetricLabels("component", "reason"),
)

func init() {
	prometheus.MustRegister(droppedEventsTotal)
}

func RecordDroppedEvent(component, reason string) {
	if component == "" {
		component = "unknown"
	}
	if reason == "" {
		reason = "unknown"
	}
	droppedEventsTotal.WithLabelValues(MetricValues(component, reason)...).Inc()
}
