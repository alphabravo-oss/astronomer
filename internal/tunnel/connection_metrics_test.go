package tunnel

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

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
