package tunnel

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// TestAgentConnectionsSurvivesDisconnect is the O-03 regression: a cluster that
// transitions connected -> disconnected must STILL emit an agent_connections
// sample (valued 0), not have its series vanish. The rewritten
// AstronomerAgentDisconnected alert keys on `astronomer_agent_connections == 0`,
// which can only fire while the series exists.
func TestAgentConnectionsSurvivesDisconnect(t *testing.T) {
	oldInstanceID := observability.InstanceID()
	observability.SetInstanceID("test-o03")
	t.Cleanup(func() { observability.SetInstanceID(oldInstanceID) })
	registerConnectionMetrics()

	cid := uuid.New()
	now := time.Now().UTC()

	// First sweep: agent connected -> gauge = 1.
	updateConnectionMetrics([]sqlc.ListClusterConnectionStatusRow{
		{ClusterID: cid, ClusterName: "prod-1", Status: "connected", LastActivity: now},
	}, now)
	if got := connectionsGaugeValue(t, cid.String()); got != 1 {
		t.Fatalf("connected gauge = %v, want 1", got)
	}

	// Second sweep: same cluster now disconnected -> series MUST persist at 0.
	updateConnectionMetrics([]sqlc.ListClusterConnectionStatusRow{
		{ClusterID: cid, ClusterName: "prod-1", Status: "disconnected", LastActivity: now.Add(-5 * time.Minute)},
	}, now)
	got, found := connectionsGaugeSample(t, cid.String())
	if !found {
		t.Fatal("O-03: agent_connections series vanished after disconnect")
	}
	if got != 0 {
		t.Fatalf("disconnected gauge = %v, want 0", got)
	}
}

func connectionsGaugeValue(t *testing.T, clusterID string) float64 {
	t.Helper()
	v, _ := connectionsGaugeSample(t, clusterID)
	return v
}

func connectionsGaugeSample(t *testing.T, clusterID string) (float64, bool) {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "astronomer_agent_connections" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["astronomer_instance_id"] == "test-o03" && labels["cluster_id"] == clusterID {
				return metric.GetGauge().GetValue(), true
			}
		}
	}
	return 0, false
}

func TestRecordAgentReconnectIncrementsStormMetric(t *testing.T) {
	oldInstanceID := observability.InstanceID()
	observability.SetInstanceID("test-reconnect-metric")
	t.Cleanup(func() {
		observability.SetInstanceID(oldInstanceID)
	})
	registerConnectionMetrics()
	agentReconnectsTotal.Reset()

	before := reconnectCounterValue(t, "cluster-a")
	recordAgentReconnect("cluster-a")
	recordAgentReconnect("cluster-a")
	after := reconnectCounterValue(t, "cluster-a")

	if after != before+2 {
		t.Fatalf("agent reconnect counter = %v, want %v", after, before+2)
	}
}

func reconnectCounterValue(t *testing.T, clusterID string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "astronomer_agent_reconnects_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["astronomer_instance_id"] == "test-reconnect-metric" && labels["cluster_id"] == clusterID {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}
