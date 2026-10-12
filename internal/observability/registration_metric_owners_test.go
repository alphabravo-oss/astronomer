package observability

import (
	"fmt"
	"testing"
)

func isolateRegistrationMetricOwners(t *testing.T) {
	t.Helper()
	baselineMetricInstance(t)
	t.Cleanup(func() { RetireRegistrationMetrics(nil, RegistrationMetricsWatermark()) })
}

func TestRegistrationMetricOwnerDeferredAndLatePublication(t *testing.T) {
	isolateRegistrationMetricOwners(t)
	hook := NewRegistrationMetricsHook()
	discarded := hook.CapturePhaseTransition("rolled-back", "", "created")
	if got := baselineMetricLabels(t, "astronomer_cluster_registration_phase"); len(got) != 0 {
		t.Fatalf("capture published before transaction commit: %v", got)
	}
	// A complete absent snapshot retires the logical owner; a late flush cannot
	// re-create its phase child, even when the same ID has a new admission.
	RetireRegistrationMetrics(nil, RegistrationMetricsWatermark())
	hook.RecordPhaseTransition("rolled-back", "", "connected")
	discarded()
	got := baselineMetricLabels(t, "astronomer_cluster_registration_phase")
	if len(got) != 1 || got[0]["phase"] != "connected" {
		t.Fatalf("late retired flush changed current owner: %v", got)
	}
	older := hook.CapturePhaseTransition("rolled-back", "connected", "provisioning")
	newer := hook.CapturePhaseTransition("rolled-back", "provisioning", "ready")
	newer()
	older()
	got = baselineMetricLabels(t, "astronomer_cluster_registration_phase")
	if len(got) != 1 || got[0]["phase"] != "ready" {
		t.Fatalf("out-of-order flush changed committed phase: %v", got)
	}
}

func TestRegistrationMetricOwnerSnapshotFreshnessAndBounds(t *testing.T) {
	isolateRegistrationMetricOwners(t)
	hook := NewRegistrationMetricsHook()
	hook.RecordPhaseTransition("current", "", "created")
	olderSnapshot := RegistrationMetricsWatermark()
	hook.RecordPhaseTransition("current", "created", "connected")
	RetireRegistrationMetrics(nil, olderSnapshot)
	if got := baselineMetricLabels(t, "astronomer_cluster_registration_phase"); len(got) != 1 || got[0]["phase"] != "connected" {
		t.Fatalf("older snapshot retired a renewed existing owner: %v", got)
	}
	oversized := make([]string, MetricOwnerMax+1)
	if RetireRegistrationMetrics(oversized, RegistrationMetricsWatermark()) {
		t.Fatal("accepted an incomplete over-budget observation")
	}
	if got := baselineMetricLabels(t, "astronomer_cluster_registration_phase"); len(got) != 1 {
		t.Fatalf("over-budget snapshot pruned prior phase: %v", got)
	}
	RetireRegistrationMetrics([]string{"current"}, RegistrationMetricsWatermark())
	if got := baselineMetricLabels(t, "astronomer_cluster_registration_phase"); len(got) != 1 {
		t.Fatal("current owner lost on complete snapshot")
	}
	RetireRegistrationMetrics(nil, RegistrationMetricsWatermark())
	if got := baselineMetricLabels(t, "astronomer_cluster_registration_phase"); len(got) != 0 {
		t.Fatal("removed owner's phase retained")
	}
}

func TestRegistrationMetricOwnerAdmissionBudget(t *testing.T) {
	isolateRegistrationMetricOwners(t)
	hook := NewRegistrationMetricsHook()
	hook.RecordPhaseTransition("current", "", "created")
	registrationMetricOwners.Lock()
	// Fill only logical owners: this exercises the admission ceiling without
	// creating tens of thousands of unrelated Prometheus children in the test.
	for i := 0; len(registrationMetricOwners.rows) < MetricOwnerMax; i++ {
		key := registrationMetricKey{InstanceID(), fmt.Sprintf("synthetic-budget-%d", i)}
		registrationMetricOwners.rows[key] = &registrationMetricOwner{}
	}
	registrationMetricOwners.Unlock()
	for i := 0; i < 20; i++ {
		hook.RecordPhaseTransition(fmt.Sprintf("overflow-%d", i), "", "created")
	}
	hook.RecordPhaseTransition("current", "created", "connected")
	registrationMetricOwners.Lock()
	count := len(registrationMetricOwners.rows)
	registrationMetricOwners.Unlock()
	if count != MetricOwnerMax {
		t.Fatalf("owners=%d want bounded%d", count, MetricOwnerMax)
	}
	got := baselineMetricLabels(t, "astronomer_cluster_registration_phase")
	if len(got) != 1 || got[0]["phase"] != "connected" {
		t.Fatalf("admission budget changed live owner or allocated overflow children: %v", got)
	}
	RetireRegistrationMetrics(nil, RegistrationMetricsWatermark())
	hook.RecordPhaseTransition("new-after-retirement", "", "created")
	if got := baselineMetricLabels(t, "astronomer_cluster_registration_phase"); len(got) != 1 {
		t.Fatal("complete retirement did not release admission capacity")
	}
}
