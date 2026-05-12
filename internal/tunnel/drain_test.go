package tunnel

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

// FEATURES-051126 T14: Hub.Drain must close every connected agent and
// clear the agents map so subsequent Disconnect calls observe them
// already gone. The close path runs in goroutines so we give it a
// short window to land.
func TestHub_Drain_ClosesAllAgents(t *testing.T) {
	h := NewHub(slog.Default())

	// Build 5 fake AgentConnection entries directly into the map. Each
	// gets its own cancellable context so we can observe the cancel via
	// Done().
	const N = 5
	contexts := make([]context.Context, N)
	for i := 0; i < N; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		contexts[i] = ctx
		agent := &AgentConnection{
			ClusterID: clusterIDForTest(i),
			SessionID: "session-" + clusterIDForTest(i),
			Streams:   NewStreamManager(8),
			cancel:    cancel,
		}
		h.mu.Lock()
		h.agents[agent.ClusterID] = agent
		h.mu.Unlock()
	}

	drained := h.Drain()
	if drained != N {
		t.Errorf("Drain() = %d, want %d", drained, N)
	}

	// Map must be empty.
	h.mu.RLock()
	remaining := len(h.agents)
	h.mu.RUnlock()
	if remaining != 0 {
		t.Errorf("post-Drain agents map has %d entries, want 0", remaining)
	}

	// Each agent's context must have been cancelled. Drain spawns a
	// goroutine per agent so we wait briefly.
	deadline := time.Now().Add(500 * time.Millisecond)
	for i, ctx := range contexts {
		for ctx.Err() == nil && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if ctx.Err() == nil {
			t.Errorf("agent %d context still active 500ms after Drain", i)
		}
	}
}

// Drain on an empty hub is a no-op (return 0) and must not panic.
func TestHub_Drain_Empty(t *testing.T) {
	h := NewHub(slog.Default())
	if got := h.Drain(); got != 0 {
		t.Errorf("Drain() on empty hub = %d, want 0", got)
	}
}

func clusterIDForTest(i int) string {
	return "cluster-" + string(rune('a'+i))
}
