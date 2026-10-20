package observability_test

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/registration"
)

func TestRegistrationMetricProjectionKeepsDomainPhases(t *testing.T) {
	previous := observability.InstanceID()
	observability.SetInstanceID(t.Name())
	t.Cleanup(func() {
		observability.RetireRegistrationMetrics(nil, observability.RegistrationMetricsWatermark())
		observability.SetInstanceID(previous)
	})
	hook := observability.NewRegistrationMetricsHook()
	phases := []registration.Phase{registration.PhaseCreated, registration.PhaseAwaitingAgent, registration.PhaseConnected, registration.PhaseProvisioning, registration.PhaseReady, registration.PhaseFailed}
	for _, phase := range phases {
		hook.RecordPhaseTransition("domain-cluster", "", string(phase))
		families, err := prometheus.DefaultGatherer.Gather()
		if err != nil {
			t.Fatal(err)
		}
		matched := false
		for _, family := range families {
			if family.GetName() != "astronomer_cluster_registration_phase" {
				continue
			}
			for _, metric := range family.GetMetric() {
				labels := map[string]string{}
				for _, label := range metric.GetLabel() {
					labels[label.GetName()] = label.GetValue()
				}
				if labels["astronomer_instance_id"] == t.Name() && labels["cluster"] == "domain-cluster" {
					if labels["phase"] != string(phase) || metric.GetGauge().GetValue() != 1 {
						t.Fatalf("domain phase=%s metric=%v", phase, labels)
					}
					matched = true
				}
			}
		}
		if !matched {
			t.Fatalf("domain phase missing from metric: %s", phase)
		}
	}
}
