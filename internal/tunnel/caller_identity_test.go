package tunnel

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/callerid"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// TestBuildK8sRequestPayloadIdentityFromSession pins §8 item 3 at the proxy
// payload builder: identity comes from the authenticated session in context.
//
// PRE-FIX: the payload had no identity fields; downstream saw only the agent
// ServiceAccount for every request.
func TestBuildK8sRequestPayloadIdentityFromSession(t *testing.T) {
	userID := uuid.New()
	req := httptest.NewRequest("GET", "/api/v1/clusters/c1/k8s/api/v1/pods", nil)
	req = req.WithContext(appmiddleware.SetAuthenticatedUserForTest(req.Context(), &appmiddleware.AuthenticatedUser{ID: userID.String()}))

	payload, err := buildK8sRequestPayload(req)
	if err != nil {
		t.Fatalf("buildK8sRequestPayload: %v", err)
	}
	if got, want := payload.User, callerid.UserSubject(userID); got != want {
		t.Fatalf("identity = %q, want %q", got, want)
	}
	if !payload.IsUser() {
		t.Fatalf("origin = %q, want user", payload.Origin)
	}
}

// TestBuildK8sRequestPayloadUnattributedIsNotMachine is invariant §7.4: the
// absence of a user must NOT be read as "machine". A route that forgets to
// populate identity has to look unattributed, not exempt.
func TestBuildK8sRequestPayloadUnattributedIsNotMachine(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/clusters/c1/k8s/api/v1/pods", nil)
	payload, err := buildK8sRequestPayload(req)
	if err != nil {
		t.Fatalf("buildK8sRequestPayload: %v", err)
	}
	if payload.IsMachine() {
		t.Fatalf("no user must not imply machine, got %+v", payload.CallerIdentity)
	}
	if payload.IsUser() || payload.Attributed() {
		t.Fatalf("expected an unattributed identity, got %+v", payload.CallerIdentity)
	}
}

// TestStartExecSessionPopulatesIdentity and its logs counterpart cover the two
// stream originators named in §8 item 3.
func TestStartExecSessionPopulatesIdentity(t *testing.T) {
	hub := NewHub(nil)
	clusterID := uuid.NewString()
	hub.RegisterAgentForTest(clusterID)
	out := hub.OutboundForTest(clusterID)
	userID := uuid.New()

	stream, err := hub.StartExecSession(callerid.WithUser(context.Background(), userID), clusterID, protocol.ExecStartPayload{
		Namespace: "default", Pod: "p", Container: "c",
	})
	if err != nil {
		t.Fatalf("StartExecSession: %v", err)
	}
	defer stream.Cancel()

	msg := <-out
	var sent protocol.ExecStartPayload
	mustUnmarshal(t, msg.Payload, &sent)
	if got, want := sent.User, callerid.UserSubject(userID); got != want {
		t.Fatalf("exec identity = %q, want %q", got, want)
	}
	if !sent.IsUser() {
		t.Fatalf("exec origin = %q, want user", sent.Origin)
	}
}

func TestStartLogStreamPopulatesIdentity(t *testing.T) {
	hub := NewHub(nil)
	clusterID := uuid.NewString()
	hub.RegisterAgentForTest(clusterID)
	out := hub.OutboundForTest(clusterID)
	userID := uuid.New()

	stream, err := hub.StartLogStream(callerid.WithUser(context.Background(), userID), clusterID, protocol.LogStartPayload{
		Namespace: "default", Pod: "p", Container: "c",
	})
	if err != nil {
		t.Fatalf("StartLogStream: %v", err)
	}
	defer stream.Cancel()

	msg := <-out
	var sent protocol.LogStartPayload
	mustUnmarshal(t, msg.Payload, &sent)
	if got, want := sent.User, callerid.UserSubject(userID); got != want {
		t.Fatalf("logs identity = %q, want %q", got, want)
	}
	if !sent.IsUser() {
		t.Fatalf("logs origin = %q, want user", sent.Origin)
	}
}

// TestStartExecSessionKeepsCallerSuppliedIdentity proves the Fill (not Resolve)
// semantics: a front door that already stamped an identity — including a §10
// machine exemption such as the kubectl-shell relay — is not overwritten by
// whatever happens to be in ctx.
func TestStartExecSessionKeepsCallerSuppliedIdentity(t *testing.T) {
	hub := NewHub(nil)
	clusterID := uuid.NewString()
	hub.RegisterAgentForTest(clusterID)
	out := hub.OutboundForTest(clusterID)

	preset := callerid.Machine(callerid.SourceKubectlShell)
	_, err := hub.StartExecSession(callerid.WithUser(context.Background(), uuid.New()), clusterID, protocol.ExecStartPayload{
		Namespace: "default", Pod: "p", Container: "c", CallerIdentity: preset,
	})
	if err != nil {
		t.Fatalf("StartExecSession: %v", err)
	}
	msg := <-out
	var sent protocol.ExecStartPayload
	mustUnmarshal(t, msg.Payload, &sent)
	if sent.User != preset.User || !sent.IsMachine() {
		t.Fatalf("preset identity was overwritten: %+v", sent.CallerIdentity)
	}
}

// captureRespondingAgent registers a fake agent that records every message the
// hub dispatches to it AND answers it, so a handler's full round trip completes.
// registerRespondingAgent (internal_door_audit_test.go) only answers; the
// identity assertions below need the message itself.
func captureRespondingAgent(t *testing.T, hub *Hub, clusterID string, respJSON []byte) (<-chan *protocol.Message, func()) {
	t.Helper()
	agent := &AgentConnection{
		ClusterID: clusterID,
		Streams:   NewStreamManager(256),
		sendCh:    make(chan *protocol.Message, sendChannelSize),
	}
	hub.agents.Set(clusterID, agent)

	seen := make(chan *protocol.Message, 8)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case msg := <-agent.sendCh:
				select {
				case seen <- msg:
				default:
				}
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
	return seen, func() { close(done) }
}

// TestInternalK8sHandlerForwardsIdentityToAgent is the receiving half of the HA
// hop (§10.3), driven through the REAL handler rather than through the helper
// the handler happens to call.
//
// The cross-pod door is TRANSPORT, not origin, so:
//   - a user identity stamped by the calling replica must reach the agent
//     unchanged, not be replaced by the door's own machine marker; and
//   - an unattributed payload genuinely started at an internal caller on the far
//     side, so the door supplies the cross-pod-transport marker.
//
// PRE-FIX: the only coverage of this asserted on callerid.Fill with hand-built
// arguments and never invoked InternalK8sHandler.Handle at all — deleting the
// whole identity block from the handler left every package still green.
func TestInternalK8sHandlerForwardsIdentityToAgent(t *testing.T) {
	respJSON, err := json.Marshal(protocol.K8sResponsePayload{StatusCode: 200})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	userSubject := callerid.UserSubject(uuid.New())
	tests := []struct {
		name       string
		sent       protocol.CallerIdentity
		wantUser   string
		wantOrigin protocol.CallerOrigin
	}{
		{
			name:       "a forwarded user identity survives the hop verbatim",
			sent:       protocol.CallerIdentity{User: userSubject, Origin: protocol.OriginUser, RequestID: "req-1"},
			wantUser:   userSubject,
			wantOrigin: protocol.OriginUser,
		},
		{
			name:       "an unattributed payload gets the transport marker",
			sent:       protocol.CallerIdentity{},
			wantUser:   callerid.MachineSubject(callerid.SourceCrossPodTransport),
			wantOrigin: protocol.OriginMachine,
		},
		{
			name:       "a machine identity from the far side survives too",
			sent:       callerid.Machine(callerid.SourceArgoCDProxy),
			wantUser:   callerid.MachineSubject(callerid.SourceArgoCDProxy),
			wantOrigin: protocol.OriginMachine,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hub := NewHub(slog.Default())
			clusterID := uuid.NewString()
			seen, stop := captureRespondingAgent(t, hub, clusterID, respJSON)
			defer stop()

			h := NewInternalK8sHandler(hub, "the-right-psk", slog.Default())
			router := chi.NewRouter()
			router.Post("/internal/tunnel/k8s/{cluster_id}", h.Handle)

			req := internalK8sForwardRequest(clusterID, protocol.K8sRequestPayload{
				Method:         http.MethodGet,
				Path:           "/api/v1/namespaces/default/pods",
				CallerIdentity: tt.sent,
			}, uuid.NewString())
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
			}

			var msg *protocol.Message
			select {
			case msg = <-seen:
			case <-time.After(5 * time.Second):
				t.Fatal("no message reached the agent")
			}
			var forwarded protocol.K8sRequestPayload
			mustUnmarshal(t, msg.Payload, &forwarded)

			if forwarded.User != tt.wantUser {
				t.Fatalf("identity on the wire = %q, want %q (payload=%+v)", forwarded.User, tt.wantUser, forwarded.CallerIdentity)
			}
			if forwarded.Origin != tt.wantOrigin {
				t.Fatalf("origin on the wire = %q, want %q", forwarded.Origin, tt.wantOrigin)
			}
			if tt.sent.RequestID != "" && forwarded.RequestID != tt.sent.RequestID {
				t.Fatalf("request id = %q, want %q", forwarded.RequestID, tt.sent.RequestID)
			}
		})
	}
}

// TestInternalK8sHandlerIgnoresHeaderSuppliedIdentity pins §7 invariant 3 at the
// cross-pod door: the identity comes from the PSK-authenticated sibling's
// payload and from nothing else. A header on this request — the one surface an
// attacker who reached the door could control — must not become an identity.
func TestInternalK8sHandlerIgnoresHeaderSuppliedIdentity(t *testing.T) {
	respJSON, err := json.Marshal(protocol.K8sResponsePayload{StatusCode: 200})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	hub := NewHub(slog.Default())
	clusterID := uuid.NewString()
	seen, stop := captureRespondingAgent(t, hub, clusterID, respJSON)
	defer stop()

	h := NewInternalK8sHandler(hub, "the-right-psk", slog.Default())
	router := chi.NewRouter()
	router.Post("/internal/tunnel/k8s/{cluster_id}", h.Handle)

	req := internalK8sForwardRequest(clusterID, protocol.K8sRequestPayload{
		Method: http.MethodGet,
		Path:   "/api/v1/namespaces/default/pods",
	}, uuid.NewString())
	req.Header.Set("Impersonate-User", "system:masters")
	req.Header.Set("X-Remote-User", "root")
	// The forwarded-user header IS trusted for the AUDIT row (it comes from the
	// PSK-authenticated sibling), but it must not silently become the payload
	// identity — that would be a header-carried identity by another name.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var msg *protocol.Message
	select {
	case msg = <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("no message reached the agent")
	}
	var forwarded protocol.K8sRequestPayload
	mustUnmarshal(t, msg.Payload, &forwarded)
	if forwarded.IsUser() {
		t.Fatalf("a header minted a user identity: %+v", forwarded.CallerIdentity)
	}
	if forwarded.User != callerid.MachineSubject(callerid.SourceCrossPodTransport) {
		t.Fatalf("identity = %q, want the transport marker", forwarded.User)
	}
}

func mustUnmarshal(t *testing.T, data []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}
