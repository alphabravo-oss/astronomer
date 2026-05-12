package tunnel

import (
	"hash/fnv"
	"sync"
)

// Sharded agent map.
//
// Before: a single Hub.mu RWMutex guarded both the agents map and the
// publisher field. Every SendToAgent grabbed mu.RLock per call; every
// register/unregister grabbed mu.Lock. At 500+ agents under heavy
// state-update traffic, this serialised the entire hub on one mutex.
//
// After: 16 independent shards, each its own RWMutex + map keyed by
// clusterID's FNV-1a hash. SendToAgent("cluster-A") and
// SendToAgent("cluster-B") contend only if they land in the same shard
// (1/16 = 6.25% probability). Drain + ConnectedClusters still walk
// every shard, but they grab each shard's lock briefly in turn — the
// total lock-hold time is the same; only contention drops.
//
// 16 was picked as a power-of-two so the modulo can be replaced with a
// mask if profiling ever shows it matters. It's far more than the
// 1-2 agents most installs run, and far less than a per-cluster lock
// (which would defeat the bulk-iteration paths).

const agentShardCount = 16

type agentShard struct {
	mu     sync.RWMutex
	agents map[string]*AgentConnection
}

type shardedAgents struct {
	shards [agentShardCount]*agentShard
}

func newShardedAgents() *shardedAgents {
	s := &shardedAgents{}
	for i := range s.shards {
		s.shards[i] = &agentShard{agents: make(map[string]*AgentConnection)}
	}
	return s
}

// shardFor maps a clusterID to its shard. FNV-1a is fast, allocation-
// free, and has decent spread for our string shape (UUIDs).
func (s *shardedAgents) shardFor(clusterID string) *agentShard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(clusterID))
	return s.shards[h.Sum32()%agentShardCount]
}

// Get returns the registered agent for a clusterID or nil.
func (s *shardedAgents) Get(clusterID string) *AgentConnection {
	sh := s.shardFor(clusterID)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	return sh.agents[clusterID]
}

// Set registers an agent under its clusterID. Returns the previous
// agent (if any) so the caller can compare/replace under their own
// lifecycle logic without re-acquiring the lock.
func (s *shardedAgents) Set(clusterID string, agent *AgentConnection) (prev *AgentConnection) {
	sh := s.shardFor(clusterID)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	prev = sh.agents[clusterID]
	sh.agents[clusterID] = agent
	return prev
}

// Delete removes an agent. Returns the value that was deleted (or nil).
func (s *shardedAgents) Delete(clusterID string) *AgentConnection {
	sh := s.shardFor(clusterID)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	prev := sh.agents[clusterID]
	delete(sh.agents, clusterID)
	return prev
}

// DeleteIfSame removes an agent only if the currently-registered entry
// is the same pointer. Replaces the open-coded "race-free remove" the
// removeAgent helper used to do under the single global mutex.
// Returns true if the delete happened.
func (s *shardedAgents) DeleteIfSame(clusterID string, expected *AgentConnection) bool {
	sh := s.shardFor(clusterID)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if current, ok := sh.agents[clusterID]; ok && current == expected {
		delete(sh.agents, clusterID)
		return true
	}
	return false
}

// Snapshot returns a copy of every (clusterID, agent) pair across all
// shards. Locks each shard briefly in turn; the returned slice is safe
// to iterate outside the lock.
func (s *shardedAgents) Snapshot() []*AgentConnection {
	// Pre-size assuming the map is ~uniformly distributed.
	out := make([]*AgentConnection, 0, agentShardCount*4)
	for _, sh := range s.shards {
		sh.mu.RLock()
		for _, agent := range sh.agents {
			out = append(out, agent)
		}
		sh.mu.RUnlock()
	}
	return out
}

// DrainAll snapshots then clears every shard. Mirrors what Hub.Drain
// wants in one round-trip so callers don't have to lock twice. The
// returned slice is the set that was removed; callers close those
// connections.
func (s *shardedAgents) DrainAll() []*AgentConnection {
	out := make([]*AgentConnection, 0, agentShardCount*4)
	for _, sh := range s.shards {
		sh.mu.Lock()
		for clusterID, agent := range sh.agents {
			out = append(out, agent)
			delete(sh.agents, clusterID)
		}
		sh.mu.Unlock()
	}
	return out
}

// ConnectedIDs returns the slice of clusterIDs with active connections.
// Snapshots under each shard's lock; the returned slice is independent.
func (s *shardedAgents) ConnectedIDs() []string {
	out := make([]string, 0, agentShardCount*4)
	for _, sh := range s.shards {
		sh.mu.RLock()
		for clusterID := range sh.agents {
			out = append(out, clusterID)
		}
		sh.mu.RUnlock()
	}
	return out
}

// Len returns the total registered agent count across all shards. This
// scans every shard with the read lock; cheap but not zero-cost — call
// it sparingly.
func (s *shardedAgents) Len() int {
	n := 0
	for _, sh := range s.shards {
		sh.mu.RLock()
		n += len(sh.agents)
		sh.mu.RUnlock()
	}
	return n
}
