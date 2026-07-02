package tunnel

import "github.com/alphabravocompany/astronomer-go/pkg/protocol"

// DrainAgentSendForTest non-blockingly drains and returns every message the
// server has enqueued to the agent's send channel (via SendToAgent). Test-only;
// lets a test assert that e.g. a MsgK8sStreamStop was sent when a watch client
// disconnected. Pairs with RegisterAgentForTest.
func (h *Hub) DrainAgentSendForTest(clusterID string) []*protocol.Message {
	agent := h.agents.Get(clusterID)
	if agent == nil {
		return nil
	}
	var out []*protocol.Message
	for {
		select {
		case msg := <-agent.sendCh:
			out = append(out, msg)
		default:
			return out
		}
	}
}
