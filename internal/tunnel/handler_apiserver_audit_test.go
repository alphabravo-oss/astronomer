package tunnel

import (
	"context"
	"encoding/json"
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
}

func (p *recordingAuditPersister) PersistAuditEvents(_ context.Context, clusterID uuid.UUID, events []json.RawMessage) (int, int, error) {
	p.calls++
	p.clusterID = clusterID
	p.events = events
	return len(events), 0, nil
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
