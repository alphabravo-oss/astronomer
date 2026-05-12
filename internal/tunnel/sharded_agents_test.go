package tunnel

import (
	"testing"
)

// Round-trip: Set + Get under multiple clusterIDs returns the right
// agent each time.
func TestShardedAgents_SetGet(t *testing.T) {
	s := newShardedAgents()
	a := &AgentConnection{ClusterID: "a"}
	b := &AgentConnection{ClusterID: "b"}

	if prev := s.Set("a", a); prev != nil {
		t.Errorf("Set(a) returned non-nil prev on empty shard: %v", prev)
	}
	if prev := s.Set("b", b); prev != nil {
		t.Errorf("Set(b) returned non-nil prev on empty shard: %v", prev)
	}
	if got := s.Get("a"); got != a {
		t.Errorf("Get(a) = %v, want %v", got, a)
	}
	if got := s.Get("b"); got != b {
		t.Errorf("Get(b) = %v, want %v", got, b)
	}
	if got := s.Get("missing"); got != nil {
		t.Errorf("Get(missing) = %v, want nil", got)
	}
}

// Set returning the previous entry is load-bearing: the reconnect path
// in HandleWebSocket uses it to detect "old session being replaced".
func TestShardedAgents_SetReturnsPrev(t *testing.T) {
	s := newShardedAgents()
	a1 := &AgentConnection{ClusterID: "x", SessionID: "old"}
	a2 := &AgentConnection{ClusterID: "x", SessionID: "new"}
	s.Set("x", a1)
	prev := s.Set("x", a2)
	if prev != a1 {
		t.Errorf("Set returned prev=%v, want %v", prev, a1)
	}
	if got := s.Get("x"); got != a2 {
		t.Errorf("Get(x) after replace = %v, want %v", got, a2)
	}
}

// DeleteIfSame must only remove when the registered pointer matches —
// guards against removing a replacement.
func TestShardedAgents_DeleteIfSame(t *testing.T) {
	s := newShardedAgents()
	a1 := &AgentConnection{ClusterID: "x"}
	a2 := &AgentConnection{ClusterID: "x"} // different pointer, same key
	s.Set("x", a1)
	s.Set("x", a2)
	// a1 is no longer registered; DeleteIfSame(x, a1) must NOT delete.
	if removed := s.DeleteIfSame("x", a1); removed {
		t.Error("DeleteIfSame removed a replacement; should have left it alone")
	}
	if got := s.Get("x"); got != a2 {
		t.Errorf("after stale DeleteIfSame, Get(x) = %v, want %v", got, a2)
	}
	// Now DeleteIfSame(x, a2) succeeds.
	if removed := s.DeleteIfSame("x", a2); !removed {
		t.Error("DeleteIfSame(x, a2) returned false; expected the live entry to be removed")
	}
	if got := s.Get("x"); got != nil {
		t.Errorf("after DeleteIfSame, Get(x) = %v, want nil", got)
	}
}

// Snapshot + ConnectedIDs cover the cross-shard iteration paths.
// At least one entry must land in each shard hash for the test to be
// meaningful — seed enough distinct keys to spread.
func TestShardedAgents_SnapshotAndConnectedIDs(t *testing.T) {
	s := newShardedAgents()
	const N = 64
	for i := 0; i < N; i++ {
		key := "cluster-" + string(rune('a'+i%26)) + "-" + string(rune('0'+i/26))
		s.Set(key, &AgentConnection{ClusterID: key})
	}

	snap := s.Snapshot()
	if len(snap) != N {
		t.Errorf("Snapshot returned %d entries, want %d", len(snap), N)
	}
	ids := s.ConnectedIDs()
	if len(ids) != N {
		t.Errorf("ConnectedIDs returned %d entries, want %d", len(ids), N)
	}
	if total := s.Len(); total != N {
		t.Errorf("Len = %d, want %d", total, N)
	}
}

// DrainAll empties the store and returns the drained set.
func TestShardedAgents_DrainAll(t *testing.T) {
	s := newShardedAgents()
	const N = 32
	for i := 0; i < N; i++ {
		key := "k-" + string(rune('a'+i%26)) + "-" + string(rune('0'+i/26))
		s.Set(key, &AgentConnection{ClusterID: key})
	}
	drained := s.DrainAll()
	if len(drained) != N {
		t.Errorf("DrainAll returned %d, want %d", len(drained), N)
	}
	if remaining := s.Len(); remaining != 0 {
		t.Errorf("post-DrainAll Len = %d, want 0", remaining)
	}
	// Second DrainAll on the now-empty store is a no-op.
	if again := s.DrainAll(); len(again) != 0 {
		t.Errorf("DrainAll on empty store returned %d entries, want 0", len(again))
	}
}

// Same key always maps to the same shard — basic determinism check.
func TestShardedAgents_ShardForDeterministic(t *testing.T) {
	s := newShardedAgents()
	first := s.shardFor("some-cluster-id")
	for i := 0; i < 100; i++ {
		if s.shardFor("some-cluster-id") != first {
			t.Fatal("shardFor returned different shard on subsequent call")
		}
	}
}
