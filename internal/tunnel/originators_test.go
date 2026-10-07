package tunnel

import (
	"context"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestSendHelmRequestAcceptsHistory(t *testing.T) {
	hub := NewHub(nil)
	_, err := hub.SendHelmRequest(context.Background(), "disconnected-cluster", protocol.MsgHelmHistory, protocol.HelmRequestPayload{})
	if err == nil || !strings.Contains(err.Error(), "cluster agent not connected") {
		t.Fatalf("history should pass message validation and reach connection lookup, got %v", err)
	}
}
