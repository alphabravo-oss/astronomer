package tunnel

// C2t (M6): negative tests proving the internal cross-pod door is no
// longer a blind spot in the audit trail. Before this change a mutation
// forwarded through /internal/tunnel/{k8s,helm}/{cluster_id} performed a
// real cluster mutation but emitted NO audit row — the originating user
// was invisible. These tests assert that a mutation through the door now
// emits a user-attributed cluster.{k8s,helm}_proxy.forwarded row, and
// that a read does not (so we don't flood the trail with non-mutations).

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// capturingAuditWriter records every row Record falls back to writing. The
// audit package's sync fallback path writes through CreateAuditLogV1 when
// no async Writer is installed (the case in unit tests).
type capturingAuditWriter struct {
	mu   sync.Mutex
	rows []sqlc.CreateAuditLogV1Params
}

func (w *capturingAuditWriter) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.rows = append(w.rows, arg)
	return nil
}

func (w *capturingAuditWriter) snapshot() []sqlc.CreateAuditLogV1Params {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]sqlc.CreateAuditLogV1Params(nil), w.rows...)
}

// registerRespondingAgent registers a fake agent for clusterID that, for
// every message the handler dispatches to it, writes respJSON back onto
// the matching stream so the handler's round-trip completes. Returns a
// stop func the test defers.
func registerRespondingAgent(t *testing.T, hub *Hub, clusterID string, respJSON []byte) func() {
	t.Helper()
	agent := &AgentConnection{
		ClusterID: clusterID,
		Streams:   NewStreamManager(256),
		sendCh:    make(chan *protocol.Message, sendChannelSize),
	}
	hub.agents.Set(clusterID, agent)

	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case msg := <-agent.sendCh:
				stream, ok := agent.Streams.GetStream(msg.StreamID)
				if !ok {
					continue
				}
				select {
				case stream.DataCh <- respJSON:
				case <-done:
					return
				}
			}
		}
	}()
	return func() { close(done) }
}

func internalK8sForwardRequest(clusterID string, payload protocol.K8sRequestPayload, userID string) *http.Request {
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/k8s/"+clusterID, bytes.NewReader(body))
	req.Header.Set(InternalSourceHeader, InternalSourceValue)
	req.Header.Set(InternalPSKHeader, "the-right-psk")
	if userID != "" {
		req.Header.Set(InternalForwardedUserHeader, userID)
	}
	return req
}

// TestInternalK8sDoor_MutationEmitsUserAttributedAuditRow is the M6
// negative test: a DELETE crossing the internal door must now leave a
// cluster.k8s_proxy.forwarded row attributed to the forwarded user.
func TestInternalK8sDoor_MutationEmitsUserAttributedAuditRow(t *testing.T) {
	hub := NewHub(slog.Default())
	clusterID := uuid.New().String()
	userID := uuid.New()

	resp, _ := json.Marshal(protocol.K8sResponsePayload{StatusCode: 200})
	stop := registerRespondingAgent(t, hub, clusterID, resp)
	defer stop()

	audit := &capturingAuditWriter{}
	h := NewInternalK8sHandler(hub, "the-right-psk", slog.Default())
	h.SetAuditWriter(audit)

	router := chi.NewRouter()
	router.Post("/internal/tunnel/k8s/{cluster_id}", h.Handle)

	req := internalK8sForwardRequest(clusterID, protocol.K8sRequestPayload{
		Method: http.MethodDelete,
		Path:   "/api/v1/namespaces/default/pods/example",
	}, userID.String())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	rows := audit.snapshot()
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want exactly 1: %#v", len(rows), rows)
	}
	row := rows[0]
	if row.Action != "cluster.k8s_proxy.forwarded" {
		t.Fatalf("audit action = %q, want cluster.k8s_proxy.forwarded", row.Action)
	}
	if !row.UserID.Valid || row.UserID.Bytes != [16]byte(userID) {
		t.Fatalf("audit user = %+v, want %s", row.UserID, userID)
	}
	if row.ResourceID != clusterID {
		t.Fatalf("audit resource id = %q, want %q", row.ResourceID, clusterID)
	}
	var detail map[string]any
	if err := json.Unmarshal(row.Detail, &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if detail["method"] != http.MethodDelete || detail["proxy"] != "internal_door" {
		t.Fatalf("audit detail = %#v", detail)
	}
}

// TestInternalK8sDoor_ReadEmitsNoAuditRow confirms only mutations are
// audited — a GET through the door leaves no row.
func TestInternalK8sDoor_ReadEmitsNoAuditRow(t *testing.T) {
	hub := NewHub(slog.Default())
	clusterID := uuid.New().String()

	resp, _ := json.Marshal(protocol.K8sResponsePayload{StatusCode: 200})
	stop := registerRespondingAgent(t, hub, clusterID, resp)
	defer stop()

	audit := &capturingAuditWriter{}
	h := NewInternalK8sHandler(hub, "the-right-psk", slog.Default())
	h.SetAuditWriter(audit)

	router := chi.NewRouter()
	router.Post("/internal/tunnel/k8s/{cluster_id}", h.Handle)

	req := internalK8sForwardRequest(clusterID, protocol.K8sRequestPayload{
		Method: http.MethodGet,
		Path:   "/api/v1/namespaces/default/pods",
	}, uuid.New().String())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if rows := audit.snapshot(); len(rows) != 0 {
		t.Fatalf("read emitted %d audit rows, want 0: %#v", len(rows), rows)
	}
}

// TestInternalHelmDoor_MutationEmitsUserAttributedAuditRow is the helm
// counterpart: a HELM_UPGRADE through the door must leave a
// cluster.helm_proxy.forwarded row attributed to the forwarded user.
func TestInternalHelmDoor_MutationEmitsUserAttributedAuditRow(t *testing.T) {
	hub := NewHub(slog.Default())
	clusterID := uuid.New().String()
	userID := uuid.New()

	resp, _ := json.Marshal(protocol.HelmResultPayload{Success: true, ReleaseName: "kube-prom-stack", Namespace: "monitoring"})
	stop := registerRespondingAgent(t, hub, clusterID, resp)
	defer stop()

	audit := &capturingAuditWriter{}
	h := NewInternalHelmHandler(hub, "the-right-psk", slog.Default())
	h.SetAuditWriter(audit)

	router := chi.NewRouter()
	router.Post("/internal/tunnel/helm/{cluster_id}", h.Handle)

	body, _ := json.Marshal(InternalHelmRequest{
		MsgType: protocol.MsgHelmUpgrade,
		Payload: protocol.HelmRequestPayload{ReleaseName: "kube-prom-stack", Namespace: "monitoring"},
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/helm/"+clusterID, bytes.NewReader(body))
	req.Header.Set(InternalSourceHeader, InternalSourceValue)
	req.Header.Set(InternalPSKHeader, "the-right-psk")
	req.Header.Set(InternalForwardedUserHeader, userID.String())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	rows := audit.snapshot()
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want exactly 1: %#v", len(rows), rows)
	}
	row := rows[0]
	if row.Action != "cluster.helm_proxy.forwarded" {
		t.Fatalf("audit action = %q, want cluster.helm_proxy.forwarded", row.Action)
	}
	if !row.UserID.Valid || row.UserID.Bytes != [16]byte(userID) {
		t.Fatalf("audit user = %+v, want %s", row.UserID, userID)
	}
	if row.ResourceID != clusterID {
		t.Fatalf("audit resource id = %q, want %q", row.ResourceID, clusterID)
	}
	if row.ResourceName != "monitoring/kube-prom-stack" {
		t.Fatalf("audit resource name = %q", row.ResourceName)
	}
}

// TestInternalHelmDoor_StatusEmitsNoAuditRow confirms a read-only
// HELM_STATUS through the door is not audited as a mutation.
func TestInternalHelmDoor_StatusEmitsNoAuditRow(t *testing.T) {
	hub := NewHub(slog.Default())
	clusterID := uuid.New().String()

	resp, _ := json.Marshal(protocol.HelmResultPayload{Success: true})
	stop := registerRespondingAgent(t, hub, clusterID, resp)
	defer stop()

	audit := &capturingAuditWriter{}
	h := NewInternalHelmHandler(hub, "the-right-psk", slog.Default())
	h.SetAuditWriter(audit)

	router := chi.NewRouter()
	router.Post("/internal/tunnel/helm/{cluster_id}", h.Handle)

	body, _ := json.Marshal(InternalHelmRequest{
		MsgType: protocol.MsgHelmStatus,
		Payload: protocol.HelmRequestPayload{ReleaseName: "kube-prom-stack", Namespace: "monitoring"},
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/helm/"+clusterID, bytes.NewReader(body))
	req.Header.Set(InternalSourceHeader, InternalSourceValue)
	req.Header.Set(InternalPSKHeader, "the-right-psk")
	req.Header.Set(InternalForwardedUserHeader, uuid.New().String())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if rows := audit.snapshot(); len(rows) != 0 {
		t.Fatalf("status emitted %d audit rows, want 0: %#v", len(rows), rows)
	}
}
