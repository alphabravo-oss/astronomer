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

	agentTunnelSendDroppedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Subsystem: "agent",
			Name:      "tunnel_send_dropped_total",
			Help:      "Outbound tunnel frames the agent could not queue, by send-path class and reason. A non-zero control-class count means the tunnel was force-closed.",
		},
		observability.MetricLabels("class", "reason"),
	)

	agentTunnelSendQueueDepth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Subsystem: "agent",
			Name:      "tunnel_send_queue_depth",
			Help:      "Occupancy of the agent's outbound tunnel queues, sampled on enqueue, by send-path class.",
		},
		observability.MetricLabels("class"),
	)

	agentInflightActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Subsystem: "agent",
			Name:      "inflight_dispatch_active",
			Help:      "Inbound tunnel messages currently being handled, by dispatch class. Sustained saturation of the buffered class means requests are being shed.",
		},
		observability.MetricLabels("class"),
	)

	agentInflightRejectedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Subsystem: "agent",
			Name:      "inflight_dispatch_rejected_total",
			Help:      "Inbound tunnel messages rejected because the agent was at its in-flight limit, by dispatch class and message type.",
		},
		observability.MetricLabels("class", "type"),
	)

	agentResponseBufferBytes = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Subsystem: "agent",
			Name:      "response_buffer_bytes",
			Help:      "Bytes currently reserved against the agent-wide buffered-response budget. Approaching the budget means large proxied responses are being refused with 429.",
		},
	)
)

func init() {
	prometheus.MustRegister(
		agentStateUpdatesReceivedTotal,
		agentStateUpdatesHandledTotal,
		agentTunnelSendDroppedTotal,
		agentTunnelSendQueueDepth,
		agentInflightActive,
		agentInflightRejectedTotal,
		agentResponseBufferBytes,
	)
}
