package handler

import (
	"context"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// TestWatchPodsSendsStreamStopOnDisconnect verifies that when the SSE watch
// consumer goes away (client navigates off the pods page → ctx cancelled), the
// pod-watch goroutine signals the agent with MsgK8sStreamStop so the agent
// cancels its kube-apiserver watch instead of leaking a goroutine + watch +
// tunnel bandwidth. Before the fix, only the local stream was closed and no
// message was ever sent to the agent.
//
// Depends on tunnel.Hub.DrainAgentSendForTest (see wiring_needed) to observe
// what was queued for the agent's WebSocket.
func TestWatchPodsSendsStreamStopOnDisconnect(t *testing.T) {
	hub := tunnel.NewHub(nil)
	hub.RegisterAgentForTest("c1")
	r := NewTunnelK8sRequester(hub)

	ctx, cancel := context.WithCancel(context.Background())

	out, err := r.WatchPods(ctx, "c1", "ns")
	if err != nil {
		t.Fatalf("WatchPods: %v", err)
	}

	// Client disconnects.
	cancel()

	// The watch goroutine must exit and close its channel.
	select {
	case _, ok := <-out:
		if ok {
			// Drain any buffered events until close.
			for range out {
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch goroutine did not exit on client disconnect")
	}

	// The agent must have been told to stop the upstream watch.
	deadline := time.After(5 * time.Second)
	for {
		msgs := hub.DrainAgentSendForTest("c1")
		for _, m := range msgs {
			if m.Type == protocol.MsgK8sStreamStop {
				return // success
			}
		}
		select {
		case <-deadline:
			t.Fatal("no MsgK8sStreamStop sent to agent after client disconnect")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
