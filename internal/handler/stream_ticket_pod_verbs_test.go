package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

// A stream ticket that can be MINTED must be REDEEMABLE, and vice versa: the
// issuance gate here and the redemption gate in the WebSocket consumers have to
// speak the same vocabulary or the terminal/log pane either 401s after a
// successful mint or hands out a credential the relay refuses. These tests drive
// the real ticket endpoint and the real consumers against identical bindings and
// assert the two decisions agree — plus that they agree on the RIGHT answer.

type fakeStreamBindings struct{ list []rbac.RoleBinding }

func (f fakeStreamBindings) GetUserBindings(_ context.Context, _ string) ([]rbac.RoleBinding, error) {
	return f.list, nil
}

// streamVerbFixture is one shipped role's exact grant set plus the stream access
// it must end up with. allowPodWatch is the SECOND redeemer of the `logs` ticket
// kind — the pod SSE watch GET /clusters/{id}/pods/watch/, gated on pods:read
// (internal/server/routes.go), which the frontend opens with a 'logs' ticket
// (openPodsWatch in frontend/src/lib/api/k8s-watch.ts). Issuance must satisfy
// the WEAKEST redeemer, so a role that can only drive the watch must still be
// able to mint.
type streamVerbFixture struct {
	name          string
	binding       rbac.RoleBinding
	allowExec     bool
	allowLogs     bool
	allowPodWatch bool
}

func streamVerbFixtures(clusterID uuid.UUID) []streamVerbFixture {
	return []streamVerbFixture{
		{
			// 098 'Platform Operator' / templates/platform-operator.yaml:
			// clusters:update but no pods rule at all. The AUTHORIZED
			// privilege reduction — it used to pass both gates.
			name: "Platform Operator",
			binding: rbac.RoleBinding{RoleRules: []rbac.Rule{
				{Resource: "clusters", Verbs: []string{"create", "read", "update", "list"}},
				{Resource: "agents", Verbs: []string{"create", "read", "update", "list"}},
				{Resource: "workloads", Verbs: []string{"read", "list"}},
				{Resource: "audit_logs", Verbs: []string{"read", "list"}},
			}},
			allowExec:     false,
			allowLogs:     false,
			allowPodWatch: false,
		},
		{
			// 098 'Monitoring Viewer': the clusters:read-without-pods shape
			// shared by every audit / GitOps / catalog viewer.
			name: "Monitoring Viewer",
			binding: rbac.RoleBinding{RoleRules: []rbac.Rule{
				{Resource: "monitoring", Verbs: []string{"read", "list"}},
				{Resource: "clusters", Verbs: []string{"read", "list"}},
			}},
			allowExec:     false,
			allowLogs:     false,
			allowPodWatch: false,
		},
		{
			// 032 'Cluster Troubleshooter': pods:exec + pods:logs, no
			// clusters:update. The parity half — it used to be 403'd on exec.
			name: "Cluster Troubleshooter",
			binding: rbac.RoleBinding{ClusterID: clusterID.String(), RoleRules: []rbac.Rule{
				{Resource: "clusters", Verbs: []string{"read"}},
				{Resource: "pods", Verbs: []string{"read", "list", "watch", "logs", "exec", "proxy"}},
			}},
			allowExec:     true,
			allowLogs:     true,
			allowPodWatch: true,
		},
		{
			// 098 'Logging Viewer': pods:logs only, never exec.
			name: "Logging Viewer",
			binding: rbac.RoleBinding{RoleRules: []rbac.Rule{
				{Resource: "logging", Verbs: []string{"read", "list"}},
				{Resource: "clusters", Verbs: []string{"read", "list"}},
				{Resource: "pods", Verbs: []string{"read", "list", "logs"}},
			}},
			allowExec:     false,
			allowLogs:     true,
			allowPodWatch: true,
		},
		{
			// 098 'Node Operator': pods:[read,list,watch] and clusters:read,
			// with `logs` omitted on purpose. It cannot stream ws/logs — but
			// it CAN drive the pod SSE watch, which redeems a 'logs' ticket
			// behind pods:read. Gating issuance on pods:logs would silently
			// drop it (and 'Storage Manager', and every pods:read persona)
			// onto the UI's polling fallback. This is the fixture that fails
			// if issuance stops matching the weakest redeemer.
			name: "Node Operator",
			binding: rbac.RoleBinding{ClusterID: clusterID.String(), RoleRules: []rbac.Rule{
				{Resource: "clusters", Verbs: []string{"read"}},
				{Resource: "nodes", Verbs: []string{"read", "list", "update", "cordon", "drain"}},
				{Resource: "pods", Verbs: []string{"read", "list", "watch"}},
			}},
			allowExec:     false,
			allowLogs:     false,
			allowPodWatch: true,
		},
	}
}

// issueStreamTicket reports whether the ticket endpoint mints a `kind` ticket
// for clusterID under the given bindings.
func issueStreamTicket(t *testing.T, bindings []rbac.RoleBinding, kind string, clusterID uuid.UUID) bool {
	t.Helper()
	h := NewStreamTicketHandler(auth.NewStreamTicketStore(time.Minute))
	h.SetAuthorization(rbac.NewEngine(), fakeStreamBindings{list: bindings})

	body := `{"stream_type":"` + kind + `","cluster_id":"` + clusterID.String() + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/streams/tickets/", bytes.NewBufferString(body))
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: uuid.NewString()}))
	w := httptest.NewRecorder()
	h.Create(w, req)

	switch w.Code {
	case http.StatusCreated:
		return true
	case http.StatusForbidden:
		return false
	default:
		t.Fatalf("%s ticket: unexpected status %d body=%s", kind, w.Code, w.Body.String())
		return false
	}
}

// redeemStream reports whether the WebSocket consumer lets the request past its
// RBAC gate. Auth is left unwired (dev/test mode admits the caller), so the only
// thing under test is authorizeCluster: a denied request is a 403, an allowed one
// falls through to websocket.Accept and fails the handshake with a 4xx that is
// not 403.
func redeemStream(t *testing.T, bindings []rbac.RoleBinding, kind string, clusterID uuid.UUID) bool {
	t.Helper()
	engine := rbac.NewEngine()
	querier := fakeStreamBindings{list: bindings}

	r := chi.NewRouter()
	switch kind {
	case auth.StreamKindExec:
		ec := tunnel.NewExecConsumer(nil, nil)
		ec.SetAuthorization(engine, querier)
		r.Get("/ws/exec/{cluster_id}/{namespace}/{pod}/{container}/", ec.HandleExec)
	case auth.StreamKindLogs:
		lc := tunnel.NewLogsConsumer(nil, nil)
		lc.SetAuthorization(engine, querier)
		r.Get("/ws/logs/{cluster_id}/{namespace}/{pod}/{container}/", lc.HandleLogs)
	default:
		t.Fatalf("unsupported stream kind %q", kind)
	}

	req := httptest.NewRequest(http.MethodGet, "/ws/"+kind+"/"+clusterID.String()+"/default/example/app/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code != http.StatusForbidden
}

// redeemPodWatch reports whether the pod SSE watch route lets the request past
// its RBAC gate. It is the OTHER redeemer of a StreamKindLogs ticket, so it is
// mounted here with the same middleware routes.go uses — requireListPermission
// resolves to middleware.RequireListPermission whenever the engine is wired.
func redeemPodWatch(t *testing.T, bindings []rbac.RoleBinding, clusterID uuid.UUID) bool {
	t.Helper()
	r := chi.NewRouter()
	r.With(middleware.RequireListPermission(
		rbac.NewEngine(), fakeStreamBindings{list: bindings}, rbac.ResourcePods, rbac.VerbRead, false,
	)).Get("/clusters/{cluster_id}/pods/watch/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/clusters/"+clusterID.String()+"/pods/watch/", nil)
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: uuid.NewString()}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code != http.StatusForbidden
}

// TestStreamTicketIssuanceMatchesWSRedemption is the both-directions agreement
// check: for every fixture, minting and redeeming must reach the same verdict,
// and that verdict must be the intended one.
//
// `exec` has one redeemer, so it is a plain equality. `logs` has TWO — ws/logs
// and the pod SSE watch — so the invariant is that issuance matches the WEAKEST
// of them: mintable exactly when at least one redeemer would accept. A stricter
// mint is a silent feature outage (the UI drops to polling); a looser one grants
// nothing, because each redeemer re-checks its own gate.
func TestStreamTicketIssuanceMatchesWSRedemption(t *testing.T) {
	clusterID := uuid.New()
	for _, fx := range streamVerbFixtures(clusterID) {
		bindings := []rbac.RoleBinding{fx.binding}

		t.Run(fx.name+"/"+auth.StreamKindExec, func(t *testing.T) {
			issued := issueStreamTicket(t, bindings, auth.StreamKindExec, clusterID)
			redeemed := redeemStream(t, bindings, auth.StreamKindExec, clusterID)
			if issued != redeemed {
				t.Fatalf("issuance/redemption disagree: minted=%v redeemable=%v", issued, redeemed)
			}
			if issued != fx.allowExec {
				t.Fatalf("exec access = %v, want %v", issued, fx.allowExec)
			}
		})

		t.Run(fx.name+"/"+auth.StreamKindLogs, func(t *testing.T) {
			issued := issueStreamTicket(t, bindings, auth.StreamKindLogs, clusterID)
			wsLogs := redeemStream(t, bindings, auth.StreamKindLogs, clusterID)
			podWatch := redeemPodWatch(t, bindings, clusterID)

			if wsLogs != fx.allowLogs {
				t.Fatalf("ws/logs access = %v, want %v", wsLogs, fx.allowLogs)
			}
			if podWatch != fx.allowPodWatch {
				t.Fatalf("pods/watch access = %v, want %v", podWatch, fx.allowPodWatch)
			}
			if want := wsLogs || podWatch; issued != want {
				t.Fatalf("logs ticket minted=%v but redeemers say ws/logs=%v pods/watch=%v; "+
					"issuance must match the weakest redeemer", issued, wsLogs, podWatch)
			}
		})
	}
}
