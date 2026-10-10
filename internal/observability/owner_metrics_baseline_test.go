package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func baselineMetricLabels(t *testing.T, familyName string) []map[string]string {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var values []map[string]string
	for _, family := range families {
		if family.GetName() != familyName {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["astronomer_instance_id"] == InstanceID() {
				values = append(values, labels)
			}
		}
	}
	return values
}

func baselineMetricInstance(t *testing.T) {
	t.Helper()
	previous := InstanceID()
	SetInstanceID(t.Name())
	t.Cleanup(func() { SetInstanceID(previous) })
}

func TestRegistrationMetricsPhaseHasOneCurrentSeries(t *testing.T) {
	baselineMetricInstance(t)
	hook := NewRegistrationMetricsHook()
	hook.RecordPhaseTransition("synthetic-current-cluster", "", "created")
	hook.RecordPhaseTransition("synthetic-current-cluster", "created", "awaiting_agent")
	hook.RecordPhaseTransition("synthetic-current-cluster", "awaiting_agent", "connected")
	labels := baselineMetricLabels(t, "astronomer_cluster_registration_phase")
	if len(labels) != 1 || labels[0]["phase"] != "connected" {
		t.Fatalf("registration retains historical phase labels: %v", labels)
	}
}

func TestRegistrationMetricsDurationBoundsOutcome(t *testing.T) {
	baselineMetricInstance(t)
	hook := NewRegistrationMetricsHook()
	for _, outcome := range []string{"synthetic-one", "synthetic-two", "synthetic-three"} {
		hook.RecordDuration("synthetic-current-cluster", outcome, false, 1)
	}
	labels := baselineMetricLabels(t, "astronomer_cluster_registration_duration_seconds")
	if len(labels) != 1 || labels[0]["outcome"] != "other" {
		t.Fatalf("registration accepts unbounded outcome labels: %v", labels)
	}
}

func TestVaultResolveMetricsBoundsOutcome(t *testing.T) {
	baselineMetricInstance(t)
	for _, outcome := range []string{"synthetic-one", "synthetic-two", "synthetic-three"} {
		RecordVaultResolve("synthetic-existing-vault", outcome, 0)
	}
	labels := baselineMetricLabels(t, "astronomer_vault_resolves_total")
	if len(labels) != 1 || labels[0]["outcome"] != "other" {
		t.Fatalf("Vault accepts unbounded outcome labels: %v", labels)
	}
}
