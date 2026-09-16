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
	tunnelHeartbeatsPersistedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Subsystem: "tunnel",
			Name:      "heartbeats_persisted_total",
			Help:      "Outcome counts for atomic agent heartbeat persistence.",
		},
		observability.MetricLabels("outcome"),
	)
	tunnelHeartbeatsLifecycleTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Subsystem: "tunnel",
			Name:      "heartbeat_lifecycle_checks_total",
			Help:      "Heartbeat lifecycle checks split by durable pending-work gate outcome.",
		},
		observability.MetricLabels("outcome"),
	)
)

func init() {
	prometheus.MustRegister(
		tunnelStateUpdatesReceivedTotal,
		tunnelStateUpdatesHandledTotal,
		tunnelHeartbeatsPersistedTotal,
		tunnelHeartbeatsLifecycleTotal,
	)
}
