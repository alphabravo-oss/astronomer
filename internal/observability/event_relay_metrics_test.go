package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestEventRelayMetrics(t *testing.T) {
	oldInstanceID := InstanceID()
	SetInstanceID("test-event-relay-metrics")
	t.Cleanup(func() { SetInstanceID(oldInstanceID) })

	SetEventRelayQueue(7, 32)
	SetEventRelayHealth(true)
	SetEventRelayLastSuccess(time.Unix(1234, 0))
	before := metricFamilyCounterValue(t, "astronomer_event_relay_events_total", "result", EventRelayResultDropped)
	RecordEventRelayResult(EventRelayResultDropped)
	ObserveEventRelayPublish(time.Now().Add(-time.Millisecond))

	if got := metricFamilyGaugeValue(t, "astronomer_event_relay_queue_depth"); got != 7 {
		t.Fatalf("queue depth = %v, want 7", got)
	}
	if got := metricFamilyGaugeValue(t, "astronomer_event_relay_queue_capacity"); got != 32 {
		t.Fatalf("queue capacity = %v, want 32", got)
	}
	if got := metricFamilyGaugeValue(t, "astronomer_event_relay_healthy"); got != 1 {
		t.Fatalf("healthy = %v, want 1", got)
	}
	if got := metricFamilyGaugeValue(t, "astronomer_event_relay_last_success_timestamp_seconds"); got != 1234 {
		t.Fatalf("last success = %v, want 1234", got)
	}
	after := metricFamilyCounterValue(t, "astronomer_event_relay_events_total", "result", EventRelayResultDropped)
	if after != before+1 {
		t.Fatalf("dropped counter = %v, want %v", after, before+1)
	}
}

func metricFamilyGaugeValue(t *testing.T, name string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			if metric.GetGauge() != nil && metric.GetLabel()[0].GetValue() == InstanceID() {
				return metric.GetGauge().GetValue()
			}
		}
	}
	return 0
}

func metricFamilyCounterValue(t *testing.T, name, labelName, labelValue string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			matched := false
			instanceMatched := false
			for _, label := range metric.Label {
				if label.GetName() == labelName && label.GetValue() == labelValue {
					matched = true
				}
				if label.GetName() == instanceIDLabel && label.GetValue() == InstanceID() {
					instanceMatched = true
				}
			}
			if matched && instanceMatched && metric.GetCounter() != nil {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}
