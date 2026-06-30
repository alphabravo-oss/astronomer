package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// TestSendHeartbeatEmitsDegradedBeatOnPartialCollect proves the H11 fix at the
// agent boundary: when an apiserver List fails (here, Nodes().List) while the
// tunnel + heartbeat loop are healthy, the agent emits a MINIMAL liveness frame
// carrying DegradedReasons instead of sending nothing. The previously-failing
// inventory field stays at its zero value (the server keeps last-good, L11).
func TestSendHeartbeatEmitsDegradedBeatOnPartialCollect(t *testing.T) {
	cs := fake.NewClientset()
	cs.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("nodes is forbidden: RBAC list verb removed")
	})

	hr := NewHealthReporter(cs, slog.Default(), 30, 60)

	var sent []*protocol.Message
	sendFn := func(m *protocol.Message) error {
		sent = append(sent, m)
		return nil
	}

	hr.sendHeartbeat(context.Background(), sendFn)

	if len(sent) != 1 {
		t.Fatalf("sendHeartbeat sent %d frames, want exactly 1 (a degraded beat, not nothing)", len(sent))
	}
	if sent[0].Type != protocol.MsgHeartbeat {
		t.Fatalf("frame type = %s, want %s", sent[0].Type, protocol.MsgHeartbeat)
	}

	var hb protocol.HeartbeatPayload
	if err := json.Unmarshal(sent[0].Payload, &hb); err != nil {
		t.Fatalf("decode heartbeat: %v", err)
	}
	if len(hb.DegradedReasons) == 0 {
		t.Fatalf("expected DegradedReasons on a partial-collect beat, got none: %+v", hb)
	}
	foundNodes := false
	for _, r := range hb.DegradedReasons {
		if len(r) >= len("list_nodes_failed") && r[:len("list_nodes_failed")] == "list_nodes_failed" {
			foundNodes = true
		}
	}
	if !foundNodes {
		t.Fatalf("expected a list_nodes_failed reason, got %v", hb.DegradedReasons)
	}
	// Failed inventory stays zero so the server's keep-last-good preserves prior values.
	if hb.NodeCount != 0 {
		t.Fatalf("NodeCount = %d, want 0 on a failed list_nodes collect", hb.NodeCount)
	}
}
