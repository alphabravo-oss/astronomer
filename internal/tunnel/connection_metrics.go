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
	ListClusterConnectionStatus(ctx context.Context, arg sqlc.ListClusterConnectionStatusParams) ([]sqlc.ListClusterConnectionStatusRow, error)
}

func registerConnectionMetrics() {
	registerConnectionMetricsOnce.Do(func() {
		prometheus.MustRegister(
			agentConnectionsGauge,
			agentLastSeenSecondsGauge,
			agentMessagesTotal,
			agentReconnectsTotal,
			connectionMetricAdmissions,
			connectionMetricObservations,
			connectionMetricLastComplete,
		)
	})
}

func updateConnectionMetrics(rows []sqlc.ListClusterConnectionStatusRow, now time.Time) {
	publishConnectionMetrics(rows, now, connectionMetricsWatermark())
}

// StartConnectionMetricsReporter periodically republishes the DB-backed
// per-cluster agent connection state into Prometheus gauges. It emits a sample
// for every non-decommissioned cluster (connected or not) so the disconnect
// alert has a series to fire on (O-03).
func StartConnectionMetricsReporter(ctx context.Context, lister clusterConnectionStatusLister, log *slog.Logger) {
	if lister == nil {
		return
	}
	go RunConnectionMetricsReporter(ctx, lister, log)
}

// RunConnectionMetricsReporter blocks until ctx is cancelled so the server
// runtime supervisor can join it before closing PostgreSQL.
func RunConnectionMetricsReporter(ctx context.Context, lister clusterConnectionStatusLister, log *slog.Logger) {
	if lister == nil {
		return
	}
	RunConnectionMetricsCollector(ctx, func(ctx context.Context) ([]sqlc.ListClusterConnectionStatusRow, error) {
		return collectConnectionMetricsPages(ctx, lister)
	}, log)
}

func collectConnectionMetricsPages(ctx context.Context, lister clusterConnectionStatusLister) ([]sqlc.ListClusterConnectionStatusRow, error) {
	var complete []sqlc.ListClusterConnectionStatusRow
	for offset := int32(0); offset <= ConnectionMetricsMaxOwners; offset += ConnectionMetricsPageSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rows, err := lister.ListClusterConnectionStatus(ctx, sqlc.ListClusterConnectionStatusParams{
			QueryLimit: ConnectionMetricsPageSize, QueryOffset: offset,
		})
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		complete, err = AppendConnectionMetricsPage(complete, rows)
		if err != nil {
			return nil, err
		}
		if len(rows) < int(ConnectionMetricsPageSize) {
			return complete, nil
		}
	}
	return nil, ErrConnectionMetricsBudget
}

// RunConnectionMetricsCollector accepts a complete, consistent observation.
// Production supplies a bounded read-only repeatable-read DB transaction so
// concurrent row deletion cannot shift OFFSET pages and retire a current owner.
func RunConnectionMetricsCollector(ctx context.Context, collect func(context.Context) ([]sqlc.ListClusterConnectionStatusRow, error), log *slog.Logger) {
	if collect == nil {
		return
	}
	registerConnectionMetrics()

	record := func() {
		now := time.Now().UTC()
		watermark := connectionMetricsWatermark()
		complete, err := collect(ctx)
		if err != nil {
			recordConnectionMetricsFailure(err)
			if log != nil {
				log.Warn("failed to collect agent connection metrics", "error", err)
			}
			return
		}
		// Failed or cancelled collections leave the previous complete observation
		// untouched, including disconnected clusters and live counter history.
		if err := ctx.Err(); err != nil {
			recordConnectionMetricsFailure(err)
			return
		}
		publishConnectionMetrics(complete, now, watermark)
	}

	record()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	if log != nil {
		log.Debug("started agent connection metrics reporter")
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			record()
		}
	}
}
