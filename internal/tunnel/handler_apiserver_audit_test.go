package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// recordingAuditPersister captures the cluster ID and events it is asked to
// persist so the test can assert the cluster comes from the session, not the
// payload.
type recordingAuditPersister struct {
	clusterID uuid.UUID
	events    []json.RawMessage
	calls     int
	err       error
}

func (p *recordingAuditPersister) PersistAuditEvents(_ context.Context, clusterID uuid.UUID, events []json.RawMessage) (int, int, error) {
	p.calls++
	p.clusterID = clusterID
	p.events = events
	if p.err != nil {
		return 0, 0, p.err
	}
	return len(events), 0, nil
}

// failingAuditPersister always returns an error so the ack-on-failure path can
// be exercised.
type failingAuditPersister struct{ err error }

func (p *failingAuditPersister) PersistAuditEvents(_ context.Context, _ uuid.UUID, _ []json.RawMessage) (int, int, error) {
	return 0, 0, p.err
}

// drainAuditAck reads one MsgApiserverAuditAck off the connection's send channel
// (non-blocking) and decodes it.
func drainAuditAck(t *testing.T, conn *AgentConnection) (protocol.ApiserverAuditAckPayload, bool) {
	t.Helper()
	select {
	case msg := <-conn.sendCh:
		if msg.Type != protocol.MsgApiserverAuditAck {
			t.Fatalf("expected APISERVER_AUDIT_ACK, got %s", msg.Type)
		}
		var ack protocol.ApiserverAuditAckPayload
		if err := json.Unmarshal(msg.Payload, &ack); err != nil {
			t.Fatalf("unmarshal ack: %v", err)
		}
		return ack, true
	default:
		return protocol.ApiserverAuditAckPayload{}, false
	}
}

func TestHandleApiserverAuditPersistsWithSessionClusterID(t *testing.T) {
	h := NewHub(slog.Default())
	persister := &recordingAuditPersister{}
	h.SetAuditPersister(persister)

	sessionCluster := uuid.New()
	conn := &AgentConnection{
		ClusterID: sessionCluster.String(),
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}

	// The payload deliberately does NOT carry a cluster id; the server must
	// derive it from the authenticated connection.
	body, _ := json.Marshal(protocol.ApiserverAuditPayload{
		Events: []json.RawMessage{
			json.RawMessage(`{"auditID":"a1","verb":"get"}`),
			json.RawMessage(`{"auditID":"a2","verb":"list"}`),
		},
	})
	msg := &protocol.Message{Type: protocol.MsgApiserverAudit, Payload: body}

	h.handleMessage(conn, msg)

	if persister.calls != 1 {
		t.Fatalf("expected PersistAuditEvents called once, got %d", persister.calls)
	}
	if persister.clusterID != sessionCluster {
		t.Errorf("expected cluster id from session %s, got %s", sessionCluster, persister.clusterID)
	}
	if len(persister.events) != 2 {
		t.Fatalf("expected 2 events persisted, got %d", len(persister.events))
	}
}

func TestHandleApiserverAuditNilPersisterIsNoop(t *testing.T) {
	h := NewHub(slog.Default())
	// No persister wired.
	conn := &AgentConnection{
		ClusterID: uuid.New().String(),
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}
	body, _ := json.Marshal(protocol.ApiserverAuditPayload{
		Events: []json.RawMessage{json.RawMessage(`{"auditID":"a1"}`)},
	})
	// Should not panic.
	h.handleMessage(conn, &protocol.Message{Type: protocol.MsgApiserverAudit, Payload: body})
}

func TestHandleApiserverAuditInvalidClusterUUID(t *testing.T) {
	h := NewHub(slog.Default())
	persister := &recordingAuditPersister{}
	h.SetAuditPersister(persister)

	conn := &AgentConnection{
		ClusterID: "not-a-uuid",
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}
	body, _ := json.Marshal(protocol.ApiserverAuditPayload{
		Events: []json.RawMessage{json.RawMessage(`{"auditID":"a1"}`)},
	})
	h.handleMessage(conn, &protocol.Message{Type: protocol.MsgApiserverAudit, Payload: body})

	if persister.calls != 0 {
		t.Fatalf("expected no persist for invalid cluster uuid, got %d calls", persister.calls)
	}
}

func TestHandleApiserverAuditSendsOKAck(t *testing.T) {
	h := NewHub(slog.Default())
	h.SetAuditPersister(&recordingAuditPersister{})

	conn := &AgentConnection{
		ClusterID: uuid.New().String(),
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}
	body, _ := json.Marshal(protocol.ApiserverAuditPayload{
		BatchID: "batch-123",
		Events: []json.RawMessage{
			json.RawMessage(`{"auditID":"a1"}`),
			json.RawMessage(`{"auditID":"a2"}`),
		},
	})
	h.handleMessage(conn, &protocol.Message{Type: protocol.MsgApiserverAudit, Payload: body})

	ack, ok := drainAuditAck(t, conn)
	if !ok {
		t.Fatal("expected an APISERVER_AUDIT_ACK to be sent back to the agent")
	}
	if ack.BatchID != "batch-123" {
		t.Errorf("ack BatchID = %q, want batch-123", ack.BatchID)
	}
	if !ack.OK {
		t.Errorf("expected OK=true ack on successful persist, got %+v", ack)
	}
	if ack.Accepted != 2 {
		t.Errorf("ack Accepted = %d, want 2", ack.Accepted)
	}
}

func TestHandleApiserverAuditSendsFailureAck(t *testing.T) {
	h := NewHub(slog.Default())
	h.SetAuditPersister(&failingAuditPersister{err: errors.New("relation does not exist")})

	conn := &AgentConnection{
		ClusterID: uuid.New().String(),
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}
	body, _ := json.Marshal(protocol.ApiserverAuditPayload{
		BatchID: "batch-err",
		Events:  []json.RawMessage{json.RawMessage(`{"auditID":"a1"}`)},
	})
	h.handleMessage(conn, &protocol.Message{Type: protocol.MsgApiserverAudit, Payload: body})

	ack, ok := drainAuditAck(t, conn)
	if !ok {
		t.Fatal("expected a failure ack so the agent holds its checkpoint")
	}
	if ack.BatchID != "batch-err" {
		t.Errorf("ack BatchID = %q, want batch-err", ack.BatchID)
	}
	if ack.OK {
		t.Error("expected OK=false ack on persist failure")
	}
	if ack.Error == "" {
		t.Error("expected a non-empty Error on the failure ack")
	}
}

func TestHandleApiserverAuditNoBatchIDNoAck(t *testing.T) {
	// A legacy batch without a BatchID gets no ack frame (nothing is waiting).
	h := NewHub(slog.Default())
	h.SetAuditPersister(&recordingAuditPersister{})

	conn := &AgentConnection{
		ClusterID: uuid.New().String(),
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}
	body, _ := json.Marshal(protocol.ApiserverAuditPayload{
		Events: []json.RawMessage{json.RawMessage(`{"auditID":"a1"}`)},
	})
	h.handleMessage(conn, &protocol.Message{Type: protocol.MsgApiserverAudit, Payload: body})

	if _, ok := drainAuditAck(t, conn); ok {
		t.Fatal("expected no ack frame for a batch without a BatchID")
	}
}
