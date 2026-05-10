package tunnel

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	dto "github.com/prometheus/client_model/go"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

func TestUpdateConnectionMetrics(t *testing.T) {
	agentConnectionsGauge.Reset()
	agentLastSeenSecondsGauge.Reset()

	clusterA := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	clusterB := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	now := time.Date(2026, 5, 10, 1, 0, 0, 0, time.UTC)

	updateConnectionMetrics([]sqlc.AgentConnection{
		{
			ClusterID:   clusterA,
			ConnectedAt: now.Add(-20 * time.Second),
			LastPing:    pgtype.Timestamptz{Time: now.Add(-5 * time.Second), Valid: true},
		},
		{
			ClusterID:   clusterB,
			ConnectedAt: now.Add(-12 * time.Second),
		},
	}, now)

	if got := metricValue(t, agentConnectionsGauge.WithLabelValues(observability.MetricValues(clusterA.String())...)); got != 1 {
		t.Fatalf("clusterA connections = %v, want 1", got)
	}
	if got := metricValue(t, agentConnectionsGauge.WithLabelValues(observability.MetricValues(clusterB.String())...)); got != 1 {
		t.Fatalf("clusterB connections = %v, want 1", got)
	}
	if got := metricValue(t, agentLastSeenSecondsGauge.WithLabelValues(observability.MetricValues(clusterA.String())...)); got != 5 {
		t.Fatalf("clusterA last seen seconds = %v, want 5", got)
	}
	if got := metricValue(t, agentLastSeenSecondsGauge.WithLabelValues(observability.MetricValues(clusterB.String())...)); got != 12 {
		t.Fatalf("clusterB last seen seconds = %v, want 12", got)
	}

	updateConnectionMetrics(nil, now)
	if got := metricValue(t, agentConnectionsGauge.WithLabelValues(observability.MetricValues(clusterA.String())...)); got != 0 {
		t.Fatalf("clusterA connections after reset = %v, want 0", got)
	}
}

func TestRecordAgentMessage(t *testing.T) {
	agentMessagesTotal.Reset()

	recordAgentMessage("cluster-a", "inbound")
	recordAgentMessage("cluster-a", "outbound")
	recordAgentMessage("cluster-a", "outbound")

	if got := metricValue(t, agentMessagesTotal.WithLabelValues(observability.MetricValues("cluster-a", "inbound")...)); got != 1 {
		t.Fatalf("inbound counter = %v, want 1", got)
	}
	if got := metricValue(t, agentMessagesTotal.WithLabelValues(observability.MetricValues("cluster-a", "outbound")...)); got != 2 {
		t.Fatalf("outbound counter = %v, want 2", got)
	}
}

func metricValue(t *testing.T, gauge prometheusMetric) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := gauge.Write(m); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if m.Gauge == nil {
		if m.Counter == nil {
			t.Fatal("metric is neither gauge nor counter")
		}
		return m.Counter.GetValue()
	}
	return m.Gauge.GetValue()
}

type prometheusMetric interface {
	Write(*dto.Metric) error
}
