package agent

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestHandleExecResizeCountsDroppedEventWhenChannelFull(t *testing.T) {
	oldInstanceID := observability.InstanceID()
	observability.SetInstanceID("test-agent-exec")
	t.Cleanup(func() {
		observability.SetInstanceID(oldInstanceID)
	})

	handler := &ExecHandler{
		log:      slog.Default(),
		sessions: make(map[string]*execSession),
	}
	handler.sessions["stream-1"] = &execSession{
		resizeC: make(chan remotecommand.TerminalSize, 1),
	}
	handler.sessions["stream-1"].resizeC <- remotecommand.TerminalSize{Width: 80, Height: 24}

	body, err := json.Marshal(protocol.ExecResizePayload{Width: 120, Height: 40})
	if err != nil {
		t.Fatalf("marshal resize payload: %v", err)
	}

	before := execDroppedCounterValue(t, map[string]string{
		"astronomer_instance_id": "test-agent-exec",
		"component":              "agent_exec_resize",
		"reason":                 "channel_full",
	})

	if err := handler.HandleExecResize(&protocol.Message{StreamID: "stream-1", Payload: body}); err != nil {
		t.Fatalf("HandleExecResize() error = %v", err)
	}

	after := execDroppedCounterValue(t, map[string]string{
		"astronomer_instance_id": "test-agent-exec",
		"component":              "agent_exec_resize",
		"reason":                 "channel_full",
	})
	if after != before+1 {
		t.Fatalf("dropped events counter = %v, want %v", after, before+1)
	}
}

func execDroppedCounterValue(t *testing.T, wantLabels map[string]string) float64 {
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
			if execDroppedLabelsMatch(metric.GetLabel(), wantLabels) && metric.Counter != nil {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func execDroppedLabelsMatch(labels []*dto.LabelPair, want map[string]string) bool {
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
