package tunnel

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

var (
	registerConnectionMetricsOnce sync.Once

	agentConnectionsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Name:      "agent_connections",
			Help:      "Active agent connections by cluster ID.",
		},
		observability.MetricLabels("cluster_id"),
	)
	agentLastSeenSecondsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Name:      "agent_last_seen_seconds",
			Help:      "Seconds since the last observed ping for an active agent connection by cluster ID.",
		},
		observability.MetricLabels("cluster_id"),
	)
	agentMessagesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "agent_messages_total",
			Help:      "Total tunnel messages observed for an agent connection by cluster ID and direction.",
		},
		observability.MetricLabels("cluster_id", "direction"),
	)
)

type activeConnectionLister interface {
	ListActiveConnections(ctx context.Context) ([]sqlc.AgentConnection, error)
}

func registerConnectionMetrics() {
	registerConnectionMetricsOnce.Do(func() {
		prometheus.MustRegister(
			agentConnectionsGauge,
			agentLastSeenSecondsGauge,
			agentMessagesTotal,
		)
	})
}

func updateConnectionMetrics(conns []sqlc.AgentConnection, now time.Time) {
	agentConnectionsGauge.Reset()
	agentLastSeenSecondsGauge.Reset()

	for _, conn := range conns {
		clusterID := conn.ClusterID.String()
		agentConnectionsGauge.WithLabelValues(observability.MetricValues(clusterID)...).Set(1)

		lastSeen := conn.ConnectedAt
		if conn.LastPing.Valid {
			lastSeen = conn.LastPing.Time
		}
		age := now.Sub(lastSeen).Seconds()
		if age < 0 {
			age = 0
		}
		agentLastSeenSecondsGauge.WithLabelValues(observability.MetricValues(clusterID)...).Set(age)
	}
}

func recordAgentMessage(clusterID, direction string) {
	registerConnectionMetrics()
	agentMessagesTotal.WithLabelValues(observability.MetricValues(clusterID, direction)...).Inc()
}

// StartConnectionMetricsReporter periodically republishes the DB-backed active
// agent connection state into Prometheus gauges.
func StartConnectionMetricsReporter(ctx context.Context, lister activeConnectionLister, log *slog.Logger) {
	if lister == nil {
		return
	}
	registerConnectionMetrics()

	record := func() {
		conns, err := lister.ListActiveConnections(ctx)
		if err != nil {
			if log != nil {
				log.Warn("failed to collect agent connection metrics", "error", err)
			}
			return
		}
		updateConnectionMetrics(conns, time.Now().UTC())
	}

	record()

	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				record()
			}
		}
	}()

	if log != nil {
		log.Debug("started agent connection metrics reporter")
	}
}
