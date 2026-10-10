package tunnel

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

func metricOwnerInstance(t *testing.T) {
	t.Helper()
	previous := observability.InstanceID()
	observability.SetInstanceID(t.Name())
	t.Cleanup(func() {
		updateConnectionMetrics(nil, time.Now())
		observability.SetInstanceID(previous)
	})
}

func TestConnectionMetricsLateCallbacksDoNotResurrectRemovedOwner(t *testing.T) {
	metricOwnerInstance(t)
	cid := uuid.New().String()
	handles := captureAgentMetrics(cid)
	handles.reconnect.Inc()
	handles.message("inbound")
	updateConnectionMetrics(nil, time.Now())
	var callbacks sync.WaitGroup
	for range 20 {
		callbacks.Go(func() {
			for range 100 {
				handles.reconnect.Inc()
				handles.message("inbound")
				handles.message("outbound")
			}
		})
	}
	callbacks.Wait()
	for _, family := range []string{"astronomer_agent_reconnects_total", "astronomer_agent_messages_total"} {
		if connectionMetricExists(t, family, cid) {
			t.Fatalf("late callback recreated retired %s", family)
		}
	}
	connectionMetricOwners.Lock()
	_, exists := connectionMetricOwners.rows[connectionMetricKey{observability.InstanceID(), cid}]
	connectionMetricOwners.Unlock()
	if exists {
		t.Fatal("retired owner remains in the owner registry")
	}
}

func TestConnectionMetricsOlderCollectionPreservesNewAdmission(t *testing.T) {
	metricOwnerInstance(t)
	watermark := connectionMetricsWatermark()
	cid := uuid.New().String()
	handles := captureAgentMetrics(cid)
	handles.reconnect.Inc()
	publishConnectionMetrics(nil, time.Now(), watermark)
	if !connectionMetricExists(t, "astronomer_agent_reconnects_total", cid) {
		t.Fatal("older collection retired a newly admitted connection")
	}
	updateConnectionMetrics(nil, time.Now())
	if connectionMetricExists(t, "astronomer_agent_reconnects_total", cid) {
		t.Fatal("next complete collection did not retire the absent owner")
	}
}

func TestConnectionMetricsRenameAndDisconnectKeepCurrentCounters(t *testing.T) {
	metricOwnerInstance(t)
	cid := uuid.New()
	handles := captureAgentMetrics(cid.String())
	row := sqlc.ListClusterConnectionStatusRow{ClusterID: cid, ClusterName: "before", Status: "connected", LastActivity: time.Now()}
	updateConnectionMetrics([]sqlc.ListClusterConnectionStatusRow{row}, time.Now())
	handles.reconnect.Inc()
	handles.message("inbound")
	row.ClusterName, row.Status = "after", "disconnected"
	updateConnectionMetrics([]sqlc.ListClusterConnectionStatusRow{row}, time.Now())
	handles.reconnect.Inc()
	handles.message("inbound")
	if got := testutil.ToFloat64(handles.reconnect); got != 2 {
		t.Fatalf("current-owner reconnect count reset on rename/disconnect: %v", got)
	}
	if got := testutil.ToFloat64(handles.inbound); got != 2 {
		t.Fatalf("current-owner messages count reset on rename/disconnect: %v", got)
	}
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "astronomer_agent_connections" {
			continue
		}
		found := false
		for _, sample := range family.GetMetric() {
			cluster, name := "", ""
			for _, label := range sample.GetLabel() {
				switch label.GetName() {
				case "cluster_id":
					cluster = label.GetValue()
				case "cluster_name":
					name = label.GetValue()
				}
			}
			if cluster == cid.String() {
				if name != "after" || sample.GetGauge().GetValue() != 0 {
					t.Fatal("rename retained old gauge labels or disconnect lost the zero sample")
				}
				found = true
			}
		}
		if !found {
			t.Fatal("current disconnected cluster gauge disappeared")
		}
	}
}
