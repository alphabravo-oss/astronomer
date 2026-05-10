package tunnel

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

var (
	tunnelStateUpdatesReceivedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Subsystem: "tunnel",
			Name:      "state_updates_received_total",
			Help:      "Number of valid STATE_UPDATE messages received from agents by resource kind.",
		},
		observability.MetricLabels("kind"),
	)

	tunnelStateUpdatesHandledTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Subsystem: "tunnel",
			Name:      "state_updates_handled_total",
			Help:      "Outcome counts for agent STATE_UPDATE messages handled by the server tunnel hub.",
		},
		observability.MetricLabels("outcome", "kind"),
	)
)

func init() {
	prometheus.MustRegister(
		tunnelStateUpdatesReceivedTotal,
		tunnelStateUpdatesHandledTotal,
	)
}
