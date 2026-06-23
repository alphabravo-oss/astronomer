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
