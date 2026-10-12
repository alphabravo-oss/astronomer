package siem

import (
	"fmt"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

func TestSIEMMetricsProjectUnrecognizedCategoriesToOther(t *testing.T) {
	previous := observability.InstanceID()
	observability.SetInstanceID(t.Name())
	t.Cleanup(func() {
		dispatchedTotal.DeletePartialMatch(prometheus.Labels{"astronomer_instance_id": t.Name()})
		droppedTotal.DeletePartialMatch(prometheus.Labels{"astronomer_instance_id": t.Name()})
		observability.SetInstanceID(previous)
	})
	for i := range 20 {
		value := fmt.Sprintf("unsupported-%d", i)
		RecordDispatched("synthetic-current-forwarder", value, value, 1)
		RecordDropped("synthetic-current-forwarder", value, 1)
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(dispatchedTotal, droppedTotal)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		count := 0
		for _, metric := range family.Metric {
			labels := map[string]string{}
			for _, label := range metric.Label {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["astronomer_instance_id"] != t.Name() {
				continue
			}
			count++
			for _, category := range []string{"format", "outcome", "reason"} {
				if value, exists := labels[category]; exists && value != "other" {
					t.Errorf("%s exported unrecognized %s category", family.GetName(), category)
				}
			}
			if metric.GetCounter().GetValue() != 20 {
				t.Errorf("%s lost projected attempts: %v", family.GetName(), metric.GetCounter().GetValue())
			}
		}
		if count != 1 {
			t.Errorf("%s allocated %d category children; want one bounded other counter", family.GetName(), count)
		}
	}
}
