package tunnel

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

type failingConnectionMetricsPage struct {
	cancel context.CancelFunc
}

func (f failingConnectionMetricsPage) ListClusterConnectionStatus(_ context.Context, arg sqlc.ListClusterConnectionStatusParams) ([]sqlc.ListClusterConnectionStatusRow, error) {
	if arg.QueryOffset != 0 {
		f.cancel()
		return nil, errors.New("synthetic second-page failure")
	}
	rows := make([]sqlc.ListClusterConnectionStatusRow, arg.QueryLimit)
	for i := range rows {
		rows[i] = sqlc.ListClusterConnectionStatusRow{
			ClusterID: uuid.New(), ClusterName: "incomplete-page", Status: "connected", LastActivity: time.Now(),
		}
	}
	return rows, nil
}

func TestConnectionMetricsIncompletePagePreservesSnapshot(t *testing.T) {
	previousInstance := observability.InstanceID()
	observability.SetInstanceID("test-metrics-incomplete-page")
	t.Cleanup(func() { observability.SetInstanceID(previousInstance) })
	registerConnectionMetrics()
	cid := uuid.New()
	updateConnectionMetrics([]sqlc.ListClusterConnectionStatusRow{
		{ClusterID: cid, ClusterName: "retained-disconnected", Status: "disconnected", LastActivity: time.Now()},
	}, time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	RunConnectionMetricsReporter(ctx, failingConnectionMetricsPage{cancel: cancel}, nil)
	if !connectionMetricExists(t, "astronomer_agent_connections", cid.String()) {
		t.Fatal("failed collection removed the previous disconnected-cluster sample")
	}
}

func TestConnectionMetricsCompleteSnapshotRetiresRemovedCounters(t *testing.T) {
	previousInstance := observability.InstanceID()
	observability.SetInstanceID("test-metrics-retirement")
	t.Cleanup(func() { observability.SetInstanceID(previousInstance) })
	registerConnectionMetrics()
	cid := uuid.New()
	updateConnectionMetrics([]sqlc.ListClusterConnectionStatusRow{
		{ClusterID: cid, ClusterName: "current", Status: "connected", LastActivity: time.Now()},
	}, time.Now())
	recordAgentReconnect(cid.String())
	recordAgentMessage(cid.String(), "inbound")
	if !connectionMetricExists(t, "astronomer_agent_reconnects_total", cid.String()) {
		t.Fatal("current-cluster reconnect counter was not recorded")
	}
	updateConnectionMetrics(nil, time.Now())
	for _, name := range []string{"astronomer_agent_reconnects_total", "astronomer_agent_messages_total"} {
		if connectionMetricExists(t, name, cid.String()) {
			t.Errorf("removed-cluster series remains in %s", name)
		}
	}
}

func connectionMetricExists(t *testing.T, name, clusterID string) bool {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			foundCluster, foundInstance := false, false
			for _, label := range metric.GetLabel() {
				foundCluster = foundCluster || label.GetName() == "cluster_id" && label.GetValue() == clusterID
				foundInstance = foundInstance || label.GetName() == "astronomer_instance_id" && label.GetValue() == observability.InstanceID()
			}
			if foundCluster && foundInstance {
				return true
			}
		}
	}
	return false
}
