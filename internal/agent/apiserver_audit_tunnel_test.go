package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// captureSender records messages queued on the tunnel.
type captureSender struct {
	msgs []*protocol.Message
	err  error
}

func (c *captureSender) Send(msg *protocol.Message) error {
	if c.err != nil {
		return c.err
	}
	c.msgs = append(c.msgs, msg)
	return nil
}

func TestTunnelAuditSenderRoundTrip(t *testing.T) {
	cap := &captureSender{}
	s := newTunnelAuditSender(cap)

	events := []json.RawMessage{
		json.RawMessage(`{"auditID":"a1","verb":"get"}`),
		json.RawMessage(`{"auditID":"a2","verb":"list"}`),
	}
	if err := s.Send(context.Background(), events); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(cap.msgs) != 1 {
		t.Fatalf("expected 1 message queued, got %d", len(cap.msgs))
	}
	msg := cap.msgs[0]
	if msg.Type != protocol.MsgApiserverAudit {
		t.Errorf("expected type %s, got %s", protocol.MsgApiserverAudit, msg.Type)
	}

	// Round-trip: the payload must decode back to the same events, and must
	// NOT carry a cluster id (the server uses the authenticated session's).
	var got protocol.ApiserverAuditPayload
	if err := json.Unmarshal(msg.Payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(got.Events) != len(events) {
		t.Fatalf("expected %d events, got %d", len(events), len(got.Events))
	}
	for i := range events {
		if string(got.Events[i]) != string(events[i]) {
			t.Errorf("event %d: got %s, want %s", i, got.Events[i], events[i])
		}
	}
}

func TestTunnelAuditSenderEmptyBatchNoSend(t *testing.T) {
	cap := &captureSender{}
	s := newTunnelAuditSender(cap)
	if err := s.Send(context.Background(), nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(cap.msgs) != 0 {
		t.Fatalf("expected no message for empty batch, got %d", len(cap.msgs))
	}
}

func TestTunnelAuditSenderPropagatesSendError(t *testing.T) {
	cap := &captureSender{err: context.Canceled}
	s := newTunnelAuditSender(cap)
	err := s.Send(context.Background(), []json.RawMessage{json.RawMessage(`{"auditID":"a1"}`)})
	if err == nil {
		t.Fatal("expected error to propagate so the tailer holds its checkpoint")
	}
}
