package events

import (
	"context"
	"testing"
	"time"

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
	_ = bus.Subscribe(ctx, AcceptAll)

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

func TestSubscribeFiltersBeforeBoundedChannel(t *testing.T) {
	oldInstanceID := observability.InstanceID()
	observability.SetInstanceID("test-events-filter")
	t.Cleanup(func() {
		observability.SetInstanceID(oldInstanceID)
	})

	before := counterValue(t, "astronomer_event_bus_filtered_events_total", map[string]string{
		"astronomer_instance_id": "test-events-filter",
	})

	bus := NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := bus.Subscribe(ctx, func(event Event) bool {
		return event.Type == TypeClusterConnected
	})

	// More rejected events than the subscriber channel can hold must not
	// consume its capacity. The first relevant event must still arrive.
	for i := 0; i < 300; i++ {
		bus.Publish(TypeClusterMetrics, map[string]any{"n": i})
	}
	bus.Publish(TypeClusterConnected, map[string]any{"cluster_id": "c1"})

	select {
	case event := <-ch:
		if event.Type != TypeClusterConnected {
			t.Fatalf("event type = %q, want %q", event.Type, TypeClusterConnected)
		}
	case <-time.After(time.Second):
		t.Fatal("relevant event was blocked by filtered traffic")
	}

	after := counterValue(t, "astronomer_event_bus_filtered_events_total", map[string]string{
		"astronomer_instance_id": "test-events-filter",
	})
	if got := after - before; got != 300 {
		t.Fatalf("filtered events counter delta = %v, want 300", got)
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
