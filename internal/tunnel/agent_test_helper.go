package tunnel

import "github.com/alphabravocompany/astronomer-go/pkg/protocol"

// RegisterAgentForTest registers a minimally-wired agent connection for a
// cluster and returns its StreamManager so tests outside this package (the
// handler tunnel requesters) can drive DataCh/DoneCh without standing up a
// real WebSocket. The send channel is buffered so SendToAgent succeeds with
// no reader; production registration goes through the WS accept path.
//
// Exported because cross-package tests in internal/handler need it.
// Production code paths never invoke this constructor.
func (h *Hub) RegisterAgentForTest(clusterID string) *StreamManager {
	agent := &AgentConnection{
		ClusterID: clusterID,
		Streams:   NewStreamManager(256),
		sendCh:    make(chan *protocol.Message, sendChannelSize),
	}
	h.agents.Set(clusterID, agent)
	return agent.Streams
}

// SoleStreamForTest returns the single active stream, or nil if the count is
// not exactly one. Tests use it to grab the stream a requester created with a
// random ID so they can write frames onto DataCh.
func (sm *StreamManager) SoleStreamForTest() *Stream {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if len(sm.streams) != 1 {
		return nil
	}
	for _, s := range sm.streams {
		return s
	}
	return nil
}

// OutboundForTest returns the registered agent's send channel so a test can
// read the exact protocol.Message the server put on the wire. Pairs with
// RegisterAgentForTest, whose sendCh has no writePump draining it.
//
// Route-level tests in internal/server use this to assert on the
// K8sRequestPayload produced by the REAL middleware chain — a test that called
// the payload builder directly would prove nothing about the wire. Returns nil
// when no agent is registered for the cluster.
func (h *Hub) OutboundForTest(clusterID string) <-chan *protocol.Message {
	agent := h.agents.Get(clusterID)
	if agent == nil {
		return nil
	}
	return agent.sendCh
}
