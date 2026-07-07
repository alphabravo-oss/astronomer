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

	// agentConnectionsGauge is published for EVERY non-decommissioned cluster —
	// 1 when its agent is connected, 0 when it is not — so the series SURVIVES a
	// disconnect (O-03). The AstronomerAgentDisconnected alert keys on
	// `astronomer_agent_connections == 0`; the old behaviour only emitted the
	// series for connected agents, so a disconnect made the series vanish and a
	// gauge that stops existing can never satisfy a threshold.
	agentConnectionsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Name:      "agent_connections",
			Help:      "1 when a cluster's agent is connected, 0 otherwise. Emitted for all non-decommissioned clusters so the series survives disconnect.",
		},
		observability.MetricLabels("cluster_id", "cluster_name"),
	)
	agentLastSeenSecondsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Name:      "agent_last_seen_seconds",
			Help:      "Seconds since the last observed agent activity (ping/connect/disconnect) by cluster, emitted for all non-decommissioned clusters.",
		},
		observability.MetricLabels("cluster_id", "cluster_name"),
	)
	agentMessagesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "agent_messages_total",
			Help:      "Total tunnel messages observed for an agent connection by cluster ID and direction.",
		},
		observability.MetricLabels("cluster_id", "direction"),
	)

	// Counter of (re)connects keyed by cluster ID.
	// Useful both for sizing the reconnect-storm fix and for
	// alerting on a cluster that's flapping (rate > N over window).
	agentReconnectsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "agent_reconnects_total",
			Help:      "Total agent (re)connect events observed by the hub, by cluster ID.",
		},
		observability.MetricLabels("cluster_id"),
	)
)

type clusterConnectionStatusLister interface {
	ListClusterConnectionStatus(ctx context.Context) ([]sqlc.ListClusterConnectionStatusRow, error)
}

func registerConnectionMetrics() {
	registerConnectionMetricsOnce.Do(func() {
		prometheus.MustRegister(
			agentConnectionsGauge,
			agentLastSeenSecondsGauge,
			agentMessagesTotal,
			agentReconnectsTotal,
		)
	})
}

// recordAgentReconnect counts one (re)connect for the given cluster. Hub
// callers fire this whenever a fresh handshake replaces an existing
// agent entry — that's the signal you want for "is this cluster
// flapping" alerts.
func recordAgentReconnect(clusterID string) {
	registerConnectionMetrics()
	agentReconnectsTotal.WithLabelValues(observability.MetricValues(clusterID)...).Inc()
}

func updateConnectionMetrics(rows []sqlc.ListClusterConnectionStatusRow, now time.Time) {
	agentConnectionsGauge.Reset()
	agentLastSeenSecondsGauge.Reset()

	for _, row := range rows {
		clusterID := row.ClusterID.String()
		connected := 0.0
		if row.Status == "connected" {
			connected = 1.0
		}
		agentConnectionsGauge.WithLabelValues(observability.MetricValues(clusterID, row.ClusterName)...).Set(connected)

		age := now.Sub(row.LastActivity).Seconds()
		if age < 0 {
			age = 0
		}
		agentLastSeenSecondsGauge.WithLabelValues(observability.MetricValues(clusterID, row.ClusterName)...).Set(age)
	}
}

func recordAgentMessage(clusterID, direction string) {
	registerConnectionMetrics()
	agentMessagesTotal.WithLabelValues(observability.MetricValues(clusterID, direction)...).Inc()
}

// StartConnectionMetricsReporter periodically republishes the DB-backed
// per-cluster agent connection state into Prometheus gauges. It emits a sample
// for every non-decommissioned cluster (connected or not) so the disconnect
// alert has a series to fire on (O-03).
func StartConnectionMetricsReporter(ctx context.Context, lister clusterConnectionStatusLister, log *slog.Logger) {
	if lister == nil {
		return
	}
	registerConnectionMetrics()

	record := func() {
		rows, err := lister.ListClusterConnectionStatus(ctx)
		if err != nil {
			if log != nil {
				log.Warn("failed to collect agent connection metrics", "error", err)
			}
			return
		}
		updateConnectionMetrics(rows, time.Now().UTC())
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
