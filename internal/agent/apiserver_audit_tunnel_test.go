package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// captureSender records audit batches submitted to the tunnel and replies with
// a configurable ack outcome, standing in for *TunnelClient.SendAuditBatch.
type captureSender struct {
	payloads [][]byte
	batchIDs []string
	// err, if set, is returned from SendAuditBatch (e.g. queue-full / timeout /
	// disconnect) so the caller exercises the checkpoint-hold path.
	err error
}

func (c *captureSender) SendAuditBatch(_ context.Context, batchID string, payload []byte) error {
	c.batchIDs = append(c.batchIDs, batchID)
	c.payloads = append(c.payloads, payload)
	return c.err
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
	if len(cap.payloads) != 1 {
		t.Fatalf("expected 1 batch submitted, got %d", len(cap.payloads))
	}

	// Round-trip: the payload must decode back to the same events, carry a
	// BatchID matching the one passed to SendAuditBatch, and must NOT carry a
	// cluster id (the server uses the authenticated session's).
	var got protocol.ApiserverAuditPayload
	if err := json.Unmarshal(cap.payloads[0], &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got.BatchID == "" {
		t.Fatal("expected a non-empty BatchID on the payload")
	}
	if got.BatchID != cap.batchIDs[0] {
		t.Errorf("payload BatchID %q != submitted batchID %q", got.BatchID, cap.batchIDs[0])
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
	if len(cap.payloads) != 0 {
		t.Fatalf("expected no batch for empty input, got %d", len(cap.payloads))
	}
}

func TestTunnelAuditSenderPropagatesSendError(t *testing.T) {
	// A non-nil result from SendAuditBatch (ack OK=false, timeout, disconnect,
	// or queue-full) must propagate so the tailer holds its checkpoint.
	cap := &captureSender{err: context.Canceled}
	s := newTunnelAuditSender(cap)
	err := s.Send(context.Background(), []json.RawMessage{json.RawMessage(`{"auditID":"a1"}`)})
	if err == nil {
		t.Fatal("expected error to propagate so the tailer holds its checkpoint")
	}
}
