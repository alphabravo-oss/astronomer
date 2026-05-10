package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestRecordDroppedEvent(t *testing.T) {
	oldInstanceID := InstanceID()
	SetInstanceID("test-drop-metrics")
	t.Cleanup(func() {
		SetInstanceID(oldInstanceID)
	})

	before := droppedCounterValue(t, map[string]string{
		"astronomer_instance_id": "test-drop-metrics",
		"component":              "component-a",
		"reason":                 "reason-b",
	})

	RecordDroppedEvent("component-a", "reason-b")

	after := droppedCounterValue(t, map[string]string{
		"astronomer_instance_id": "test-drop-metrics",
		"component":              "component-a",
		"reason":                 "reason-b",
	})
	if after != before+1 {
		t.Fatalf("counter = %v, want %v", after, before+1)
	}
}

func droppedCounterValue(t *testing.T, wantLabels map[string]string) float64 {
	t.Helper()

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "astronomer_dropped_events_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			if dropLabelsMatch(metric.GetLabel(), wantLabels) && metric.Counter != nil {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func dropLabelsMatch(labels []*dto.LabelPair, want map[string]string) bool {
	if len(labels) != len(want) {
		return false
	}
	for _, label := range labels {
		if want[label.GetName()] != label.GetValue() {
			return false
		}
	}
	return true
}
