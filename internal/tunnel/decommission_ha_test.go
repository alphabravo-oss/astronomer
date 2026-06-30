package tunnel

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// TestSendDecommission_SiblingPodReturnsRetryable verifies the H8 HA fix: when
// the agent's WS is NOT on this pod but the locator says a SIBLING owns it,
// SendDecommission returns a retryable "cluster agent not connected" error so
// the worker re-queues the task onto the owning pod (instead of silently
// skipping managed-side cleanup ~50% of the time under HA).
func TestSendDecommission_SiblingPodReturnsRetryable(t *testing.T) {
	h := NewHub(slog.Default())
	h.SetLocator(NewFakeLocatorForTest("10.0.0.1:8000", map[string]string{
		"cid": "10.0.0.2:8000", // owned by a sibling pod
	}))

	ack, connected, err := h.SendDecommission(context.Background(), "cid", protocol.DecommissionPayload{ClusterID: "cid"}, 50*time.Millisecond)
	if err == nil {
		t.Fatalf("expected retryable error for sibling-pod ownership, got nil")
	}
	if connected {
		t.Errorf("connected should be false (agent not on this pod)")
	}
	if ack != nil {
		t.Errorf("ack should be nil")
	}
	// The error text must match the worker's isAgentNotConnectedErr matcher.
	if !strings.Contains(err.Error(), "cluster agent not connected") {
		t.Errorf("error %q must contain the agent-not-connected sentinel", err.Error())
	}
}

// TestSendDecommission_SingleReplicaSkips verifies the common single-replica
// case is unaffected: no locator entry (or an entry pointing at self) → the
// agent is genuinely gone → skip-with-audit (nil error, connected=false), NOT
// a spurious re-queue.
func TestSendDecommission_SingleReplicaSkips(t *testing.T) {
	// No locator at all (single-replica default).
	h := NewHub(slog.Default())
	if ack, connected, err := h.SendDecommission(context.Background(), "cid", protocol.DecommissionPayload{ClusterID: "cid"}, 50*time.Millisecond); err != nil || connected || ack != nil {
		t.Fatalf("no-locator: want (nil,false,nil), got (%v,%v,%v)", ack, connected, err)
	}

	// Locator present but the entry points at THIS pod (we lost the WS) →
	// also skip, no self-forward loop.
	h2 := NewHub(slog.Default())
	h2.SetLocator(NewFakeLocatorForTest("10.0.0.1:8000", map[string]string{"cid": "10.0.0.1:8000"}))
	if ack, connected, err := h2.SendDecommission(context.Background(), "cid", protocol.DecommissionPayload{ClusterID: "cid"}, 50*time.Millisecond); err != nil || connected || ack != nil {
		t.Fatalf("self-entry: want (nil,false,nil), got (%v,%v,%v)", ack, connected, err)
	}
}
