package tunnel

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

func TestConnectionMetricsAdmissionBudgetAndAtomicPublication(t *testing.T) {
	metricOwnerInstance(t)
	instance := observability.InstanceID()
	cid := uuid.New()
	row := sqlc.ListClusterConnectionStatusRow{ClusterID: cid, ClusterName: "retained", Status: "connected", LastActivity: time.Now()}
	first := captureAgentMetrics(cid.String())
	first.reconnected()
	updateConnectionMetrics([]sqlc.ListClusterConnectionStatusRow{row}, time.Unix(100, 0))
	watermark := connectionMetricsWatermark()
	// Fill logical fresh admissions without allocating 30,000 unrelated
	// Prometheus children. The allocator uses this same registry size fence.
	connectionMetricOwners.Lock()
	for len(connectionMetricOwners.rows) < ConnectionMetricsMaxOwners {
		connectionMetricOwners.serial++
		connectionMetricOwners.rows[connectionMetricKey{instance, uuid.New().String()}] = &connectionMetricOwner{created: connectionMetricOwners.serial}
	}
	connectionMetricOwners.Unlock()
	for range 20 {
		droppedID := uuid.New().String()
		dropped := captureAgentMetrics(droppedID)
		dropped.reconnected()
		dropped.message("inbound")
		dropped.message("outbound")
		if dropped.reconnect != nil || dropped.inbound != nil || dropped.outbound != nil {
			t.Fatal("over-budget admission allocated counter children")
		}
		if connectionMetricExists(t, "astronomer_agent_reconnects_total", droppedID) || connectionMetricExists(t, "astronomer_agent_messages_total", droppedID) {
			t.Fatal("dropped metrics-only admission published owner series")
		}
	}
	if got := testutil.ToFloat64(connectionMetricAdmissions.WithLabelValues(instance, "budget_dropped")); got != 20 {
		t.Fatalf("dropped metric admissions=%v want20", got)
	}
	second := captureAgentMetrics(cid.String())
	second.reconnected()
	if first.reconnect != second.reconnect || testutil.ToFloat64(second.reconnect) != 2 {
		t.Fatal("full budget reset or dropped an existing current owner")
	}
	newRow := row
	newRow.ClusterID, newRow.ClusterName = uuid.New(), "not-published"
	if publishConnectionMetrics([]sqlc.ListClusterConnectionStatusRow{row, newRow}, time.Unix(200, 0), watermark) {
		t.Fatal("overflowing combined current/fresh-admission observation was marked complete")
	}
	if connectionMetricExists(t, "astronomer_agent_connections", newRow.ClusterID.String()) {
		t.Fatal("refused observation partially published a new gauge")
	}
	if !connectionMetricExists(t, "astronomer_agent_connections", cid.String()) || testutil.ToFloat64(connectionMetricLastComplete.WithLabelValues(instance)) != 100 {
		t.Fatal("refused observation erased or falsely refreshed the previous complete snapshot")
	}
	if testutil.ToFloat64(connectionMetricObservations.WithLabelValues(instance, "owner_budget")) != 1 {
		t.Fatal("refused observation did not expose the finite owner-budget result")
	}
	connectionMetricOwners.Lock()
	count := len(connectionMetricOwners.rows)
	connectionMetricOwners.Unlock()
	if count != ConnectionMetricsMaxOwners {
		t.Fatalf("registry exceeded its hard budget: %d", count)
	}
	// A later complete observation may retire genuinely absent owners; there
	// is no age/LRU eviction and no registry of historical tombstones.
	updateConnectionMetrics([]sqlc.ListClusterConnectionStatusRow{row}, time.Unix(300, 0))
	if got := testutil.ToFloat64(first.reconnect); got != 2 {
		t.Fatalf("successful retirement reset the remaining current counter: %v", got)
	}
}
