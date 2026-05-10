package agent

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

var (
	agentStateUpdatesReceivedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Subsystem: "agent",
			Name:      "state_updates_received_total",
			Help:      "Number of Kubernetes informer events received by the agent state subscriber by resource kind.",
		},
		observability.MetricLabels("kind"),
	)

	agentStateUpdatesHandledTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Subsystem: "agent",
			Name:      "state_updates_handled_total",
			Help:      "Outcome counts for Kubernetes informer events handled by the agent state subscriber.",
		},
		observability.MetricLabels("outcome", "kind"),
	)
)

func init() {
	prometheus.MustRegister(
		agentStateUpdatesReceivedTotal,
		agentStateUpdatesHandledTotal,
	)
}
