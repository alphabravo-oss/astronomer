package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// ackMsg builds a MsgApiserverAuditAck frame for batchID with the given outcome.
func ackMsg(t *testing.T, batchID string, ok bool, errStr string) *protocol.Message {
	t.Helper()
	body, err := json.Marshal(protocol.ApiserverAuditAckPayload{BatchID: batchID, OK: ok, Error: errStr})
	if err != nil {
		t.Fatalf("marshal ack: %v", err)
	}
	return &protocol.Message{Type: protocol.MsgApiserverAuditAck, Payload: body}
}

// TestSendAuditBatchAckOK: an OK=true ack unblocks SendAuditBatch with nil so
// the tailer advances its checkpoint.
func TestSendAuditBatchAckOK(t *testing.T) {
	tc := NewTunnelClient(testConfig(), testLogger())

	done := make(chan error, 1)
	go func() {
		done <- tc.SendAuditBatch(context.Background(), "b1", []byte(`{}`))
	}()

	// Wait for the sender to register its waiter, then route the ack.
	waitForPendingAck(t, tc, "b1")
	tc.routeAuditAck(ackMsg(t, "b1", true, ""))

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected nil on OK ack, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendAuditBatch did not return after OK ack")
	}
	// The pending entry must be cleaned up.
	tc.auditAcksMu.Lock()
	_, stillPending := tc.auditAcks["b1"]
	tc.auditAcksMu.Unlock()
	if stillPending {
		t.Error("expected pending ack entry to be cleaned up")
	}
}

// TestSendAuditBatchAckFailure: an OK=false ack returns an error so the tailer
// holds its checkpoint.
func TestSendAuditBatchAckFailure(t *testing.T) {
	tc := NewTunnelClient(testConfig(), testLogger())

	done := make(chan error, 1)
	go func() {
		done <- tc.SendAuditBatch(context.Background(), "b2", []byte(`{}`))
	}()
	waitForPendingAck(t, tc, "b2")
	tc.routeAuditAck(ackMsg(t, "b2", false, "table does not exist"))

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error on OK=false ack")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendAuditBatch did not return after failure ack")
	}
}

// TestSendAuditBatchTimeout: with no ack delivered, SendAuditBatch returns an
// error after the bounded timeout.
func TestSendAuditBatchTimeout(t *testing.T) {
	tc := NewTunnelClient(testConfig(), testLogger())
	tc.auditAckTimeout = 50 * time.Millisecond

	err := tc.SendAuditBatch(context.Background(), "b3", []byte(`{}`))
	if err == nil {
		t.Fatal("expected timeout error when no ack arrives")
	}
	// Entry cleaned up after timeout.
	tc.auditAcksMu.Lock()
	_, stillPending := tc.auditAcks["b3"]
	tc.auditAcksMu.Unlock()
	if stillPending {
		t.Error("expected pending ack entry to be cleaned up after timeout")
	}
}

// TestFailAuditAckWaitersOnDisconnect: a pending waiter is failed when the
// tunnel disconnects so the sender returns immediately instead of waiting out
// its timeout.
func TestFailAuditAckWaitersOnDisconnect(t *testing.T) {
	tc := NewTunnelClient(testConfig(), testLogger())
	tc.auditAckTimeout = 10 * time.Second // long, so only the disconnect can unblock us

	done := make(chan error, 1)
	go func() {
		done <- tc.SendAuditBatch(context.Background(), "b4", []byte(`{}`))
	}()
	waitForPendingAck(t, tc, "b4")

	// Simulate the disconnect path.
	tc.failAuditAckWaiters()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error when tunnel disconnects mid-wait")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendAuditBatch did not return after disconnect")
	}
}

// waitForPendingAck blocks until a waiter for batchID has been registered.
func waitForPendingAck(t *testing.T, tc *TunnelClient, batchID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tc.auditAcksMu.Lock()
		_, ok := tc.auditAcks[batchID]
		tc.auditAcksMu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("waiter for batch %s was never registered", batchID)
}
