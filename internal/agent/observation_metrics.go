package agent

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/agent/observation"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

var agentObservationRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "astronomer", Subsystem: "agent", Name: "observation_requests_total",
	Help: "Actual shared typed observation LIST/WATCH API call results, excluding stream lifecycle events and synthetic repair.",
}, observability.MetricLabels("kind", "verb", "outcome"))

var agentObservationWatchEventsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "astronomer", Subsystem: "agent", Name: "observation_watch_events_total",
	Help: "Shared observation watch lifecycle outcomes, including synthetic repair; these are not API request counts.",
}, observability.MetricLabels("kind", "outcome"))

func init() { prometheus.MustRegister(agentObservationRequestsTotal, agentObservationWatchEventsTotal) }
func recordObservationRequest(kind observation.Kind, verb, outcome string) {
	agentObservationRequestsTotal.WithLabelValues(observability.MetricValues(string(kind), verb, outcome)...).Inc()
}

func recordObservationWatchEvent(kind observation.Kind, outcome string) {
	agentObservationWatchEventsTotal.WithLabelValues(observability.MetricValues(string(kind), outcome)...).Inc()
}
