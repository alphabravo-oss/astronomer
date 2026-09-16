package observability

import "github.com/prometheus/client_golang/prometheus"

var eventBusFilteredEventsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "astronomer",
		Subsystem: "event_bus",
		Name:      "filtered_events_total",
		Help:      "Total event-to-subscriber deliveries rejected before entering a subscriber channel.",
	},
	MetricLabels(),
)

func init() {
	prometheus.MustRegister(eventBusFilteredEventsTotal)
}

// RecordFilteredEvent records one event rejected by a subscriber's delivery
// filter. It deliberately has no event-type or subscriber labels: both sets
// can grow, while the aggregate is sufficient to measure avoided fan-out.
func RecordFilteredEvent() {
	eventBusFilteredEventsTotal.WithLabelValues(MetricValues()...).Inc()
}
