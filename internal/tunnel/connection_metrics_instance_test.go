package tunnel

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// The reporter can publish before the server resolves its persisted instance
// id. Those first series (astronomer_instance_id="unknown", agents not yet
// reconnected → 0) must be retired once the real id is known; on 2.1.0-rc.7
// they stayed at 0 forever and AstronomerAgentDisconnected fired for every
// connected cluster.
func TestConnectionMetricsRetireSeriesOfAPreviousInstanceID(t *testing.T) {
	oldInstanceID := observability.InstanceID()
	t.Cleanup(func() { observability.SetInstanceID(oldInstanceID) })
	registerConnectionMetrics()
	cid := uuid.New()
	now := time.Now().UTC()

	observability.SetInstanceID("boot-placeholder-" + cid.String())
	updateConnectionMetrics([]sqlc.ListClusterConnectionStatusRow{
		{ClusterID: cid, ClusterName: "local", Status: "disconnected", LastActivity: now},
	}, now)
	observability.SetInstanceID("persisted-" + cid.String())
	updateConnectionMetrics([]sqlc.ListClusterConnectionStatusRow{
		{ClusterID: cid, ClusterName: "local", Status: "connected", LastActivity: now},
	}, now.Add(15*time.Second))

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "astronomer_agent_connections" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["cluster_id"] == cid.String() {
				seen[labels["astronomer_instance_id"]] = metric.GetGauge().GetValue()
			}
		}
	}
	if len(seen) != 1 || seen["persisted-"+cid.String()] != 1 {
		t.Fatalf("agent_connections series for the cluster = %v, want only the persisted instance at 1", seen)
	}
}
