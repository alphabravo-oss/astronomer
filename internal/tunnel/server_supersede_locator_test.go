package tunnel

import (
	"context"
	"log/slog"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestRemoveAgentReportsSupersession verifies removeAgent's owner check: it
// returns true only when the passed connection is still the registered agent,
// and false when a newer connection has already replaced it (same-pod
// reconnect). The disconnect tail relies on this bool to avoid clobbering the
// newer connection's locator entry.
func TestRemoveAgentReportsSupersession(t *testing.T) {
	h := NewHub(slog.Default())
	const cid = "cluster-supersede"

	old := &AgentConnection{ClusterID: cid, SessionID: "old"}
	if prev := h.agents.Set(cid, old); prev != nil {
		t.Fatalf("unexpected existing agent on first Set: %v", prev)
	}

	// A newer connection lands on the same pod and replaces old in the map.
	newc := &AgentConnection{ClusterID: cid, SessionID: "new"}
	if prev := h.agents.Set(cid, newc); prev != old {
		t.Fatalf("Set should return the superseded old connection, got %v", prev)
	}

	// The superseded old goroutine's teardown must report false and must NOT
	// remove the new connection from the map.
	if h.removeAgent(old) {
		t.Fatal("removeAgent(old) returned true: a superseded connection must report false")
	}
	if got := h.GetAgent(cid); got != newc {
		t.Fatalf("new connection was clobbered by superseded teardown, got %v", got)
	}

	// The true owner's teardown reports true and clears the map.
	if !h.removeAgent(newc) {
		t.Fatal("removeAgent(newc) returned false for the registered owner")
	}
	if got := h.GetAgent(cid); got != nil {
		t.Fatalf("owner teardown left a stale entry: %v", got)
	}
}

// TestSupersededDisconnectDoesNotClobberLocator reproduces the M/race in the
// HandleWebSocket disconnect tail. On a same-pod reconnect the new connection
// re-runs loc.Set (installing a fresh refresh loop and rewriting redis to this
// pod's own address). The M10 CAS delete does NOT protect here because the
// stale value equals this pod's own address, so the compare matches and the
// key is deleted, orphaning the live cluster with no directory entry and no
// refresh loop. Gating loc.Delete on removeAgent's owner check is the fix.
func TestSupersededDisconnectDoesNotClobberLocator(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	ctx := context.Background()

	h := NewHub(slog.Default())
	const selfAddr = "10.0.0.1:8000"
	loc := newTestLocator(rdb, selfAddr)
	h.SetLocator(loc)
	const cid = "cluster-samepod-reconnect"

	// Old connection registered + published to the locator.
	old := &AgentConnection{ClusterID: cid, SessionID: "old"}
	h.agents.Set(cid, old)
	loc.Set(ctx, cid)

	// New connection lands on the SAME pod before old tears down: it replaces
	// old in the map and re-publishes (same pod address) with a fresh loop.
	newc := &AgentConnection{ClusterID: cid, SessionID: "new"}
	h.agents.Set(cid, newc)
	loc.Set(ctx, cid)

	// Old goroutine runs the disconnect tail exactly as HandleWebSocket does:
	// only clear the locator when we were still the registered owner.
	stillOwner := h.removeAgent(old)
	if stillOwner {
		loc.Delete(ctx, cid)
	}

	// The new owner's locator entry must survive.
	got, err := rdb.Get(ctx, locatorKeyPrefix+cid).Result()
	if err != nil {
		t.Fatalf("locator entry was clobbered by the superseded same-pod teardown (err=%v); siblings would 503", err)
	}
	if got != selfAddr {
		t.Fatalf("locator addr = %q, want this pod's own addr %q", got, selfAddr)
	}

	// The new connection's refresh loop must still be live.
	loc.mu.Lock()
	_, hasLoop := loc.cancels[cid]
	loc.mu.Unlock()
	if !hasLoop {
		t.Fatal("new connection's refresh loop was cancelled by the superseded teardown")
	}

	// Cleanup: cancel the live loop so miniredis shuts down cleanly.
	loc.Delete(ctx, cid)
}
