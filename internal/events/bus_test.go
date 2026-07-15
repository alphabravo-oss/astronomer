package events

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

func TestPublishCountsDroppedEventForSlowSubscriber(t *testing.T) {
	oldInstanceID := observability.InstanceID()
	observability.SetInstanceID("test-events-bus")
	t.Cleanup(func() {
		observability.SetInstanceID(oldInstanceID)
	})

	before := counterValue(t, "astronomer_dropped_events_total", map[string]string{
		"astronomer_instance_id": "test-events-bus",
		"component":              "events_bus",
		"reason":                 "slow_subscriber",
	})

	bus := NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = bus.Subscribe(ctx)

	for i := 0; i < 257; i++ {
		bus.Publish(TypeClusterConnected, map[string]any{"n": i})
	}

	after := counterValue(t, "astronomer_dropped_events_total", map[string]string{
		"astronomer_instance_id": "test-events-bus",
		"component":              "events_bus",
		"reason":                 "slow_subscriber",
	})
	if after <= before {
		t.Fatalf("dropped events counter did not increase: before=%v after=%v", before, after)
	}
}

func counterValue(t *testing.T, familyName string, wantLabels map[string]string) float64 {
	t.Helper()

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != familyName {
			continue
		}
		for _, metric := range family.GetMetric() {
			if labelsMatch(metric.GetLabel(), wantLabels) && metric.Counter != nil {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func labelsMatch(labels []*dto.LabelPair, want map[string]string) bool {
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
