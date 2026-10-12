package observability

import (
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

func isolateVaultMetricOwners(t *testing.T) {
	t.Helper()
	baselineMetricInstance(t)
	t.Cleanup(func() { RetireVaultMetrics(nil, VaultMetricsWatermark()) })
}

func vaultMetricCount(t *testing.T, name, connection, outcome string) float64 {
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
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["astronomer_instance_id"] == InstanceID() && labels["connection"] == connection && labels["outcome"] == outcome {
				if metric.Counter != nil {
					return metric.GetCounter().GetValue()
				}
				if metric.Gauge != nil {
					return metric.GetGauge().GetValue()
				}
			}
		}
	}
	return 0
}

func TestVaultMetricLeaseDeletedAndRecreatedConnection(t *testing.T) {
	isolateVaultMetricOwners(t)
	id := uuid.New()
	old := BeginVaultMetricLookup().Bind(id, "owned-connection")
	old.RecordResolve(VaultOutcomeSucceeded, .01)
	old.RecordHealth(true)
	RetireVaultMetricOwner(id)
	old.RecordResolve(VaultOutcomeFailed, 0)
	old.RecordHealth(false)
	if labels := baselineMetricLabels(t, "astronomer_vault_resolves_total"); len(labels) != 0 {
		t.Fatalf("late callback recreated retired series: %v", labels)
	}
	// Reusing a display name gets a different database owner and metric label.
	newID := uuid.New()
	current := BeginVaultMetricLookup().Bind(newID, "owned-connection")
	current.RecordResolve(VaultOutcomeSucceeded, .02)
	old.RecordResolve(VaultOutcomeSucceeded, .03)
	labels := baselineMetricLabels(t, "astronomer_vault_resolves_total")
	if len(labels) != 1 || labels[0]["connection"] != newID.String() {
		t.Fatalf("old name owner interfered with current ID: %v", labels)
	}
	if got := vaultMetricCount(t, "astronomer_vault_resolves_total", newID.String(), VaultOutcomeSucceeded); got != 1 {
		t.Fatalf("new owner inherited retired count=%v", got)
	}
}

func TestVaultMetricLookupAndSnapshotFreshness(t *testing.T) {
	isolateVaultMetricOwners(t)
	id := uuid.New()
	pending := BeginVaultMetricLookup()
	RetireVaultMetricOwner(id)
	pending.Bind(id, "owned").RecordResolve(VaultOutcomeSucceeded, .01)
	if labels := baselineMetricLabels(t, "astronomer_vault_resolves_total"); len(labels) != 0 {
		t.Fatal("deleted lookup allocated an owner after commit")
	}
	lease := BeginVaultMetricLookup().Bind(id, "owned")
	lease.RecordResolve(VaultOutcomeSucceeded, .01)
	watermark := VaultMetricsWatermark()
	renewed := BeginVaultMetricLookup().Bind(id, "owned")
	RetireVaultMetrics(nil, watermark)
	renewed.RecordResolve(VaultOutcomeSucceeded, .01)
	if got := vaultMetricCount(t, "astronomer_vault_resolves_total", id.String(), VaultOutcomeSucceeded); got != 2 {
		t.Fatalf("old observation reset current owner's count=%v", got)
	}
	// Invalid/unbounded observations never retire or invalidate a lookup.
	pending = BeginVaultMetricLookup()
	if RetireVaultMetrics(make([]VaultMetricSnapshotRow, MetricOwnerMax+1), VaultMetricsWatermark()) {
		t.Fatal("accepted over-budget snapshot")
	}
	pending.Bind(id, "owned").RecordResolve(VaultOutcomeSucceeded, .01)
	if got := vaultMetricCount(t, "astronomer_vault_resolves_total", id.String(), VaultOutcomeSucceeded); got != 3 {
		t.Fatal("over-budget snapshot changed live owner")
	}
	pending = BeginVaultMetricLookup()
	RetireVaultMetrics([]VaultMetricSnapshotRow{{ID: id, Name: "owned"}}, VaultMetricsWatermark())
	pending.Bind(uuid.New(), "late-unbound").RecordResolve(VaultOutcomeSucceeded, .01)
	if labels := baselineMetricLabels(t, "astronomer_vault_resolves_total"); len(labels) != 1 {
		t.Fatal("pre-snapshot unbound lookup created an absent owner")
	}
	RetireVaultMetrics(nil, VaultMetricsWatermark())
	lease.RecordResolve(VaultOutcomeSucceeded, .01)
	if labels := baselineMetricLabels(t, "astronomer_vault_resolves_total"); len(labels) != 0 {
		t.Fatal("complete absence did not retire prior owner")
	}
}

func TestVaultMetricOwnerAdmissionBudgetAndUnknownNames(t *testing.T) {
	isolateVaultMetricOwners(t)
	id := uuid.New()
	current := BeginVaultMetricLookup().Bind(id, "owned")
	current.RecordResolve(VaultOutcomeSucceeded, .01)
	vaultMetricOwners.Lock()
	for len(vaultMetricOwners.rows) < MetricOwnerMax {
		vaultMetricOwners.rows[vaultMetricKey{InstanceID(), uuid.New()}] = &vaultMetricOwner{}
	}
	vaultMetricOwners.Unlock()
	for i := 0; i < 20; i++ {
		BeginVaultMetricLookup().Bind(uuid.New(), "overflow").RecordHealth(true)
	}
	current = BeginVaultMetricLookup().Bind(id, "owned")
	current.RecordResolve(VaultOutcomeSucceeded, .01)
	vaultMetricOwners.Lock()
	count := len(vaultMetricOwners.rows)
	vaultMetricOwners.Unlock()
	if count != MetricOwnerMax {
		t.Fatalf("owners=%d want%d", count, MetricOwnerMax)
	}
	if got := vaultMetricCount(t, "astronomer_vault_resolves_total", id.String(), VaultOutcomeSucceeded); got != 2 {
		t.Fatal("overflow reset an existing owner")
	}
	if labels := baselineMetricLabels(t, "astronomer_vault_connection_health"); len(labels) != 0 {
		t.Fatal("overflow created health children")
	}
	for _, name := range []string{"unverified-one", "unverified-two", "unverified-three"} {
		RecordVaultResolve(name, "unknown-outcome", 0)
	}
	labels := baselineMetricLabels(t, "astronomer_vault_resolves_total")
	if len(labels) != 2 {
		t.Fatalf("unverified names created unbounded children: %v", labels)
	}
	if got := vaultMetricCount(t, "astronomer_vault_resolves_total", "unknown", "other"); got != 3 {
		t.Fatalf("unknown aggregate=%v want3", got)
	}
	RetireVaultMetrics(nil, VaultMetricsWatermark())
	BeginVaultMetricLookup().Bind(uuid.New(), "after-retirement").RecordHealth(true)
	if labels := baselineMetricLabels(t, "astronomer_vault_connection_health"); len(labels) != 1 {
		t.Fatal("retirement failed to release capacity")
	}
}
