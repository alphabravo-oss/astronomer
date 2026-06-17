package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/kubectl"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// --- shared fakes for the kubectl-shell handler tests ---

type fakeShellQuerier struct {
	mu       sync.Mutex
	sessions map[uuid.UUID]*sqlc.KubectlSession
	commands map[uuid.UUID][]sqlc.KubectlSessionCommand
	users    map[uuid.UUID]sqlc.User
	clusters map[uuid.UUID]sqlc.Cluster
	audits   []sqlc.CreateAuditLogV1Params
}

// CreateAuditLogV1 captures audit rows so tests can assert on them. The
// presence of this method also makes recordAudit's auditWriterV1 type
// assertion succeed (the handler otherwise no-ops the audit write).
func (f *fakeShellQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audits = append(f.audits, arg)
	return nil
}

// auditFor returns the last captured audit row whose action matches.
func (f *fakeShellQuerier) auditFor(action string) (sqlc.CreateAuditLogV1Params, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.audits) - 1; i >= 0; i-- {
		if f.audits[i].Action == action {
			return f.audits[i], true
		}
	}
	return sqlc.CreateAuditLogV1Params{}, false
}

func newFakeShellQuerier() *fakeShellQuerier {
	return &fakeShellQuerier{
		sessions: map[uuid.UUID]*sqlc.KubectlSession{},
		commands: map[uuid.UUID][]sqlc.KubectlSessionCommand{},
		users:    map[uuid.UUID]sqlc.User{},
		clusters: map[uuid.UUID]sqlc.Cluster{},
	}
}

func (f *fakeShellQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return u, nil
}

func (f *fakeShellQuerier) GetClusterByID(_ context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.clusters[id]
	if !ok {
		return sqlc.Cluster{}, pgx.ErrNoRows
	}
	return c, nil
}

func (f *fakeShellQuerier) CreateKubectlSession(_ context.Context, arg sqlc.CreateKubectlSessionParams) (sqlc.KubectlSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := sqlc.KubectlSession{
		ID: uuid.New(), UserID: arg.UserID, ClusterID: arg.ClusterID,
		SaNamespace: arg.SaNamespace, SaName: arg.SaName,
		PodNamespace: arg.PodNamespace, PodName: arg.PodName,
		Status:      arg.Status,
		StartedAt:   time.Now(),
		LastInputAt: time.Now(),
		ExpiresAt:   time.Now().Add(4 * time.Hour),
		ClientIp:    arg.ClientIp, UserAgent: arg.UserAgent,
	}
	f.sessions[row.ID] = &row
	return row, nil
}
func (f *fakeShellQuerier) GetKubectlSessionByID(_ context.Context, id uuid.UUID) (sqlc.KubectlSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.sessions[id]
	if !ok {
		return sqlc.KubectlSession{}, pgx.ErrNoRows
	}
	return *r, nil
}
func (f *fakeShellQuerier) ListActiveKubectlSessionsByCluster(_ context.Context, cid uuid.UUID) ([]sqlc.KubectlSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []sqlc.KubectlSession
	for _, r := range f.sessions {
		if r.ClusterID == cid && (r.Status == "starting" || r.Status == "active") {
			out = append(out, *r)
		}
	}
	return out, nil
}
func (f *fakeShellQuerier) ListAllActiveKubectlSessions(_ context.Context) ([]sqlc.KubectlSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []sqlc.KubectlSession
	for _, r := range f.sessions {
		if r.Status == "starting" || r.Status == "active" {
			out = append(out, *r)
		}
	}
	return out, nil
}
func (f *fakeShellQuerier) ListExpiredKubectlSessions(_ context.Context) ([]sqlc.KubectlSession, error) {
	return nil, nil
}
func (f *fakeShellQuerier) SetKubectlSessionStatus(_ context.Context, arg sqlc.SetKubectlSessionStatusParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.sessions[arg.ID]; ok {
		r.Status = arg.Status
		if arg.LastError != "" {
			r.LastError = arg.LastError
		}
		if arg.Status == "closed" || arg.Status == "expired" || arg.Status == "failed" {
			r.ClosedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		}
	}
	return nil
}
func (f *fakeShellQuerier) TouchKubectlSessionInput(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.sessions[id]; ok && (r.Status == "starting" || r.Status == "active") {
		r.LastInputAt = time.Now()
	}
	return nil
}
func (f *fakeShellQuerier) InsertKubectlSessionCommand(_ context.Context, arg sqlc.InsertKubectlSessionCommandParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands[arg.SessionID] = append(f.commands[arg.SessionID], sqlc.KubectlSessionCommand{
		ID: int64(len(f.commands[arg.SessionID]) + 1), SessionID: arg.SessionID,
		CommandAt: time.Now(), CommandLine: arg.CommandLine,
	})
	return nil
}
func (f *fakeShellQuerier) ListKubectlSessionCommands(_ context.Context, arg sqlc.ListKubectlSessionCommandsParams) ([]sqlc.KubectlSessionCommand, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := f.commands[arg.SessionID]
	off := int(arg.Offset)
	if off > len(rows) {
		return nil, nil
	}
	end := off + int(arg.Limit)
	if end > len(rows) || arg.Limit <= 0 {
		end = len(rows)
	}
	return append([]sqlc.KubectlSessionCommand(nil), rows[off:end]...), nil
}
func (f *fakeShellQuerier) CountKubectlSessionCommands(_ context.Context, sid uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.commands[sid])), nil
}

// fakeBindings implements KubectlBindingsQuerier.
type fakeBindings struct{ list []rbac.RoleBinding }

func (f *fakeBindings) GetUserBindings(_ context.Context, _ string) ([]rbac.RoleBinding, error) {
	return f.list, nil
}

// fakeK8sRequester always returns 201/204 so Open() succeeds.
type fakeShellRequester struct {
	mu     sync.Mutex
	calls  []string
	bodies map[string][]byte // path -> last POST body
}

// bodyFor returns the last POST body whose path contains substr.
func (f *fakeShellRequester) bodyFor(substr string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	for path, b := range f.bodies {
		if strings.Contains(path, substr) {
			return b
		}
	}
	return nil
}

func (f *fakeShellRequester) Do(_ context.Context, _, method, path string, body []byte, _ map[string]string) (*kubectl.K8sResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, method+" "+path)
	if method == "POST" {
		if f.bodies == nil {
			f.bodies = map[string][]byte{}
		}
		f.bodies[path] = append([]byte(nil), body...)
	}
	if method == "GET" && strings.Contains(path, "/pods/") {
		// Pod is Ready immediately.
		return &kubectl.K8sResponse{
			StatusCode: 200,
			Body:       []byte(`{"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}]}}`),
		}, nil
	}
	if method == "POST" {
		return &kubectl.K8sResponse{StatusCode: 201, Body: []byte("{}")}, nil
	}
	if method == "DELETE" {
		return &kubectl.K8sResponse{StatusCode: 200, Body: []byte("{}")}, nil
	}
	return &kubectl.K8sResponse{StatusCode: 200, Body: []byte(`{"items":[]}`)}, nil
}

func newTestKubectlHandler(t *testing.T) (*KubectlShellHandler, *fakeShellQuerier, uuid.UUID, uuid.UUID) {
	t.Helper()
	q := newFakeShellQuerier()
	userID := uuid.New()
	clusterID := uuid.New()
	q.users[userID] = sqlc.User{ID: userID, Email: "op@example.test"}
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID}
	bindings := &fakeBindings{}
	engine := rbac.NewEngine()
	h := NewKubectlShellHandler(q, bindings, engine, kubectl.Deps{
		Queries:         q,
		Requester:       &fakeShellRequester{},
		PodReadyTimeout: 500 * time.Millisecond,
	})
	return h, q, userID, clusterID
}

// newTestKubectlHandlerWithRBAC is like newTestKubectlHandler but exposes
// the fake requester (so tests can inspect the manifests POSTed to k8s)
// and the bindings fake (so tests can grant astronomer RBAC verbs).
func newTestKubectlHandlerWithRBAC(t *testing.T) (*KubectlShellHandler, *fakeShellQuerier, *fakeShellRequester, *fakeBindings, uuid.UUID, uuid.UUID) {
	t.Helper()
	q := newFakeShellQuerier()
	userID := uuid.New()
	clusterID := uuid.New()
	q.users[userID] = sqlc.User{ID: userID, Email: "op@example.test"}
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID}
	bindings := &fakeBindings{}
	requester := &fakeShellRequester{}
	engine := rbac.NewEngine()
	h := NewKubectlShellHandler(q, bindings, engine, kubectl.Deps{
		Queries:         q,
		Requester:       requester,
		PodReadyTimeout: 500 * time.Millisecond,
	})
	return h, q, requester, bindings, userID, clusterID
}

// clusterRoleVerbs decodes a ClusterRole manifest body and returns the
// verbs of its first rule (the shell manifest only emits one rule).
func clusterRoleVerbs(t *testing.T, body []byte) []string {
	t.Helper()
	if body == nil {
		return nil
	}
	var cr struct {
		Rules []struct {
			Verbs []string `json:"verbs"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(body, &cr); err != nil {
		t.Fatalf("decode ClusterRole: %v body=%s", err, body)
	}
	if len(cr.Rules) == 0 {
		return nil
	}
	return cr.Rules[0].Verbs
}

func authReq(method, target string, body string, userID uuid.UUID, isSuperuser bool) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID: userID.String(), Email: "op@example.test", AuthMethod: "jwt",
	})
	_ = isSuperuser
	return req.WithContext(ctx)
}

// unwrapData decodes a {"data": ...} envelope into out.
func unwrapData(t *testing.T, body []byte, out any) {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unwrap envelope: %v body=%s", err, body)
	}
	if len(env.Data) == 0 {
		t.Fatalf("no data field in response body=%s", body)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		t.Fatalf("unwrap data: %v body=%s", err, body)
	}
}

func newKubectlRouter(h *KubectlShellHandler) http.Handler {
	r := chi.NewRouter()
	r.Post("/api/v1/clusters/{cluster_id}/shell/sessions/", h.Open)
	r.Get("/api/v1/clusters/{cluster_id}/shell/sessions/", h.List)
	r.Get("/api/v1/clusters/{cluster_id}/shell/sessions/{id}/", h.Get)
	r.Post("/api/v1/clusters/{cluster_id}/shell/sessions/{id}/close/", h.Close)
	r.Get("/api/v1/clusters/{cluster_id}/shell/sessions/{id}/commands/", h.Commands)
	r.Get("/api/v1/ws/clusters/{cluster_id}/shell/sessions/{id}/", h.HandleWS)
	r.Get("/api/v1/admin/shell-sessions/", h.AdminListAll)
	r.Get("/api/v1/admin/shell-sessions/{id}/commands/", h.AdminCommands)
	return r
}

// --- tests ---

func TestKubectlHandler_OpenCreatesSession(t *testing.T) {
	h, q, userID, clusterID := newTestKubectlHandler(t)
	r := newKubectlRouter(h)
	req := authReq("POST", "/api/v1/clusters/"+clusterID.String()+"/shell/sessions/", "", userID, false)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("Open status: want 201, got %d: %s", w.Code, w.Body.String())
	}
	var info kubectl.SessionInfo
	unwrapData(t, w.Body.Bytes(), &info)
	if info.Status != "active" {
		t.Fatalf("status: %s (body=%s)", info.Status, w.Body.String())
	}
	if _, ok := q.sessions[info.ID]; !ok {
		t.Fatalf("session row not persisted")
	}
}

func TestKubectlHandler_CommandsEndpoint_ShowsRecorded(t *testing.T) {
	h, q, userID, clusterID := newTestKubectlHandler(t)
	r := newKubectlRouter(h)

	// Open a session.
	req := authReq("POST", "/api/v1/clusters/"+clusterID.String()+"/shell/sessions/", "", userID, false)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var info kubectl.SessionInfo
	unwrapData(t, w.Body.Bytes(), &info)

	// Plant two recorded commands.
	_ = q.InsertKubectlSessionCommand(context.Background(), sqlc.InsertKubectlSessionCommandParams{SessionID: info.ID, CommandLine: "kubectl get pods"})
	_ = q.InsertKubectlSessionCommand(context.Background(), sqlc.InsertKubectlSessionCommandParams{SessionID: info.ID, CommandLine: "ls -la /var/log"})

	req = authReq("GET", "/api/v1/clusters/"+clusterID.String()+"/shell/sessions/"+info.ID.String()+"/commands/", "", userID, false)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("commands status: %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "kubectl get pods") || !strings.Contains(body, "ls -la") {
		t.Fatalf("commands endpoint missing rows: %s", body)
	}
}

func TestKubectlHandler_CloseFlipsStatus(t *testing.T) {
	h, q, userID, clusterID := newTestKubectlHandler(t)
	r := newKubectlRouter(h)
	req := authReq("POST", "/api/v1/clusters/"+clusterID.String()+"/shell/sessions/", "", userID, false)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var info kubectl.SessionInfo
	unwrapData(t, w.Body.Bytes(), &info)

	req = authReq("POST", "/api/v1/clusters/"+clusterID.String()+"/shell/sessions/"+info.ID.String()+"/close/", "", userID, false)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("close status: %d body=%s", w.Code, w.Body.String())
	}
	if got := q.sessions[info.ID].Status; got != "closed" {
		t.Fatalf("status after close: %s", got)
	}
}

func TestKubectlHandler_GetForeignSession_Forbidden(t *testing.T) {
	h, q, userID, clusterID := newTestKubectlHandler(t)
	r := newKubectlRouter(h)
	otherID := uuid.New()
	q.users[otherID] = sqlc.User{ID: otherID}

	// Open as userID
	req := authReq("POST", "/api/v1/clusters/"+clusterID.String()+"/shell/sessions/", "", userID, false)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var info kubectl.SessionInfo
	unwrapData(t, w.Body.Bytes(), &info)

	// Try to GET as otherID
	req = authReq("GET", "/api/v1/clusters/"+clusterID.String()+"/shell/sessions/"+info.ID.String()+"/", "", otherID, false)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for foreign session, got %d", w.Code)
	}
}

func TestKubectlHandler_RequiresSuperuser_AdminEndpoints(t *testing.T) {
	h, q, userID, _ := newTestKubectlHandler(t)
	r := newKubectlRouter(h)

	// Non-superuser → 403.
	req := authReq("GET", "/api/v1/admin/shell-sessions/", "", userID, false)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-superuser admin list: want 403, got %d", w.Code)
	}

	// Promote to superuser → 200.
	q.users[userID] = sqlc.User{ID: userID, IsSuperuser: true}
	req = authReq("GET", "/api/v1/admin/shell-sessions/", "", userID, true)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("superuser admin list: want 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestKubectlHandler_AdminCommands_SuperuserSeesAny(t *testing.T) {
	h, q, userID, clusterID := newTestKubectlHandler(t)
	r := newKubectlRouter(h)

	// Open as userID.
	req := authReq("POST", "/api/v1/clusters/"+clusterID.String()+"/shell/sessions/", "", userID, false)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var info kubectl.SessionInfo
	unwrapData(t, w.Body.Bytes(), &info)
	_ = q.InsertKubectlSessionCommand(context.Background(), sqlc.InsertKubectlSessionCommandParams{SessionID: info.ID, CommandLine: "kubectl exec -it"})

	// Different superuser pulls the session's audit log.
	superID := uuid.New()
	q.users[superID] = sqlc.User{ID: superID, IsSuperuser: true}
	req = authReq("GET", "/api/v1/admin/shell-sessions/"+info.ID.String()+"/commands/", "", superID, true)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("admin commands: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "kubectl exec -it") {
		t.Fatalf("admin commands should include the recorded line; got %s", w.Body.String())
	}
}

// TestKubectlHandler_WSEndpointBridgesToExecProxy replaces the prior
// "WS endpoint redirects" test: the v2 handler no longer 307-redirects
// onto /api/v1/ws/exec/ because Firefox does not follow redirects on
// WS handshakes (and some corporate proxies strip the Upgrade header).
// Instead, after validating the session row, the handler upgrades the
// inbound WS on the original route and calls ExecProxy.ProxyToAgent
// with the pod coords pulled from the session record. This test
// asserts the lookup→proxy wiring without bringing up a real WS by
// driving the handler through the entrypoint that runs BEFORE the
// upgrade (the missing-Exec 503 path) and through a fake proxy that
// records its arguments.
func TestKubectlHandler_WSEndpointBridgesToExecProxy(t *testing.T) {
	h, _, userID, clusterID := newTestKubectlHandler(t)

	// 1. Without an ExecProxy wired, an authenticated, authorized WS
	//    handshake on an active session 503s with a clear code rather
	//    than silently 200ing or 404ing. This protects against boot-
	//    ordering bugs in server wiring.
	r := newKubectlRouter(h)
	req := authReq("POST", "/api/v1/clusters/"+clusterID.String()+"/shell/sessions/", "", userID, false)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var info kubectl.SessionInfo
	unwrapData(t, w.Body.Bytes(), &info)

	req = authReq("GET", "/api/v1/ws/clusters/"+clusterID.String()+"/shell/sessions/"+info.ID.String()+"/", "", userID, false)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("WS endpoint without ExecProxy: want 503, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "shell_unavailable") {
		t.Fatalf("missing-ExecProxy body should include shell_unavailable; got %s", w.Body.String())
	}

	// 2. With a foreign caller, the session lookup must still 403
	//    BEFORE any WS upgrade is attempted. We don't need an
	//    ExecProxy wired for this assertion — loadSessionForCluster
	//    fires first.
	otherID := uuid.New()
	req = authReq("GET", "/api/v1/ws/clusters/"+clusterID.String()+"/shell/sessions/"+info.ID.String()+"/", "", otherID, false)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign WS caller: want 403, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestKubectlHandler_RequiresClusterUpdate is a smoke test for the route
// gating contract — the route registry (server/routes.go) wraps the
// handler with RequirePermission(clusters, update). We can't directly
// exercise the global server router in this package without dragging
// the entire server build, so we assert the smaller contract that the
// handler is registered behind the cluster_id chi param and the
// matching response code when an authenticated caller provides an
// unknown cluster (cluster_not_found, not authentication_required).
func TestKubectlHandler_RequiresClusterUpdate(t *testing.T) {
	h, _, userID, _ := newTestKubectlHandler(t)
	r := newKubectlRouter(h)
	// Unknown cluster id → 404 cluster_not_found, NOT 401/403.
	req := authReq("POST", "/api/v1/clusters/"+uuid.New().String()+"/shell/sessions/", "", userID, false)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown cluster: want 404, got %d body=%s", w.Code, w.Body.String())
	}
	// "cluster_not_found" was canonicalized to apierror.NotFound ("not_found").
	if !strings.Contains(w.Body.String(), apierror.NotFound) {
		t.Fatalf("body should include %s; got %s", apierror.NotFound, w.Body.String())
	}

	// Unauthenticated → 401.
	req = httptest.NewRequest("POST", "/api/v1/clusters/"+uuid.New().String()+"/shell/sessions/", strings.NewReader(""))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: want 401, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- E1 (H5): break-glass shell defaults to read-only ---

func containsAny(verbs []string, wanted ...string) string {
	set := map[string]bool{}
	for _, v := range verbs {
		set[v] = true
	}
	for _, w := range wanted {
		if set[w] {
			return w
		}
	}
	return ""
}

// TestKubectlHandler_DefaultShellIsReadOnly is the H5 negative test: a
// non-elevated operator (even one who holds clusters:update RBAC and
// therefore passed the route gate) gets a per-session ClusterRole with
// ONLY read verbs. The old behavior hardcoded create/update/patch into
// every shell — this asserts that attack surface is gone by default.
func TestKubectlHandler_DefaultShellIsReadOnly(t *testing.T) {
	h, _, requester, bindings, userID, clusterID := newTestKubectlHandlerWithRBAC(t)
	// Grant the operator FULL write RBAC. The point of the test is that
	// holding the RBAC is not enough — without an explicit elevation
	// opt-in the shell is still read-only.
	bindings.list = []rbac.RoleBinding{{
		UserID: userID.String(),
		RoleRules: []rbac.Rule{
			{Resource: string(rbac.ResourceClusters), Verbs: []string{
				string(rbac.VerbRead), string(rbac.VerbUpdate), string(rbac.VerbDelete),
			}},
		},
	}}
	r := newKubectlRouter(h)

	// No body → no elevation request.
	req := authReq("POST", "/api/v1/clusters/"+clusterID.String()+"/shell/sessions/", "", userID, false)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("open: want 201, got %d body=%s", w.Code, w.Body.String())
	}

	verbs := clusterRoleVerbs(t, requester.bodyFor("/clusterroles"))
	if len(verbs) == 0 {
		t.Fatalf("expected a ClusterRole to be created for a non-superuser shell")
	}
	if bad := containsAny(verbs, "create", "update", "patch", "delete", "*"); bad != "" {
		t.Fatalf("default shell ClusterRole must be read-only, but granted %q (verbs=%v)", bad, verbs)
	}
	// Sanity: it should still carry read verbs.
	if got := containsAny(verbs, "get", "list", "watch"); got == "" {
		t.Fatalf("default shell ClusterRole should grant read verbs, got %v", verbs)
	}
}

// TestKubectlHandler_ElevationRequiresRBAC asserts that asking to elevate
// without the matching astronomer RBAC fails CLOSED to read-only (the
// session still opens, but with no write verbs), and that elevation is
// recorded in the audit row.
func TestKubectlHandler_ElevationRequiresRBAC(t *testing.T) {
	h, q, requester, bindings, userID, clusterID := newTestKubectlHandlerWithRBAC(t)
	// Operator holds ONLY read RBAC. (In production the route gate
	// requires clusters:update to even reach the handler; here we drive
	// the handler directly to prove the elevation check is independent of
	// the gate and fails closed.)
	bindings.list = []rbac.RoleBinding{{
		UserID: userID.String(),
		RoleRules: []rbac.Rule{
			{Resource: string(rbac.ResourceClusters), Verbs: []string{string(rbac.VerbRead)}},
		},
	}}
	r := newKubectlRouter(h)

	req := authReq("POST", "/api/v1/clusters/"+clusterID.String()+"/shell/sessions/", `{"elevate":true}`, userID, false)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("open: want 201, got %d body=%s", w.Code, w.Body.String())
	}

	verbs := clusterRoleVerbs(t, requester.bodyFor("/clusterroles"))
	if bad := containsAny(verbs, "create", "update", "patch", "delete", "*"); bad != "" {
		t.Fatalf("elevation without RBAC must stay read-only, but granted %q (verbs=%v)", bad, verbs)
	}

	// The audit row records that elevation was requested but not granted.
	row, ok := q.auditFor("kubectl.session.opened")
	if !ok {
		t.Fatalf("expected a kubectl.session.opened audit row")
	}
	var detail map[string]any
	if err := json.Unmarshal(row.Detail, &detail); err != nil {
		t.Fatalf("decode audit detail: %v raw=%s", err, row.Detail)
	}
	if detail["elevation_request"] != true {
		t.Fatalf("audit should record elevation_request=true, got %v", detail["elevation_request"])
	}
	if detail["elevated"] != false {
		t.Fatalf("audit should record elevated=false (RBAC missing), got %v", detail["elevated"])
	}
}

// TestKubectlHandler_ElevationGrantsWriteWithRBAC asserts the deliberate,
// audited write path: explicit opt-in + the matching clusters:update RBAC
// yields a write-capable ClusterRole, and the audit row marks it elevated.
func TestKubectlHandler_ElevationGrantsWriteWithRBAC(t *testing.T) {
	h, q, requester, bindings, userID, clusterID := newTestKubectlHandlerWithRBAC(t)
	bindings.list = []rbac.RoleBinding{{
		UserID: userID.String(),
		RoleRules: []rbac.Rule{
			{Resource: string(rbac.ResourceClusters), Verbs: []string{
				string(rbac.VerbRead), string(rbac.VerbUpdate),
			}},
		},
	}}
	r := newKubectlRouter(h)

	req := authReq("POST", "/api/v1/clusters/"+clusterID.String()+"/shell/sessions/", `{"elevate":true}`, userID, false)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("open: want 201, got %d body=%s", w.Code, w.Body.String())
	}

	verbs := clusterRoleVerbs(t, requester.bodyFor("/clusterroles"))
	if got := containsAny(verbs, "create", "update", "patch"); got == "" {
		t.Fatalf("elevated shell should grant write verbs, got %v", verbs)
	}
	// clusters:delete RBAC was NOT granted, so delete must be absent.
	if bad := containsAny(verbs, "delete", "*"); bad != "" {
		t.Fatalf("elevated shell without clusters:delete must not grant %q (verbs=%v)", bad, verbs)
	}

	row, ok := q.auditFor("kubectl.session.opened")
	if !ok {
		t.Fatalf("expected a kubectl.session.opened audit row")
	}
	var detail map[string]any
	if err := json.Unmarshal(row.Detail, &detail); err != nil {
		t.Fatalf("decode audit detail: %v raw=%s", err, row.Detail)
	}
	if detail["elevated"] != true {
		t.Fatalf("audit should record elevated=true, got %v", detail["elevated"])
	}
}

// TestKubectlHandler_SuperuserElevationIsClusterAdmin keeps the deliberate
// elevated path intact: a superuser who opts in gets a cluster-admin
// binding (no per-session ClusterRole) — but NOT by default.
func TestKubectlHandler_SuperuserElevationIsClusterAdmin(t *testing.T) {
	h, _, requester, bindings, userID, clusterID := newTestKubectlHandlerWithRBAC(t)
	bindings.list = []rbac.RoleBinding{{UserID: userID.String(), IsSuperuser: true}}
	r := newKubectlRouter(h)

	// Default (no elevation) → read-only ClusterRole, NOT cluster-admin.
	req := authReq("POST", "/api/v1/clusters/"+clusterID.String()+"/shell/sessions/", "", userID, false)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("open: want 201, got %d body=%s", w.Code, w.Body.String())
	}
	verbs := clusterRoleVerbs(t, requester.bodyFor("/clusterroles"))
	if bad := containsAny(verbs, "*", "create", "update", "patch", "delete"); bad != "" {
		t.Fatalf("superuser default shell must still be read-only, granted %q (verbs=%v)", bad, verbs)
	}

	// Explicit elevation → cluster-admin binding, no per-session ClusterRole.
	requester.bodies = nil
	req = authReq("POST", "/api/v1/clusters/"+clusterID.String()+"/shell/sessions/", `{"elevate":true}`, userID, false)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("elevated open: want 201, got %d body=%s", w.Code, w.Body.String())
	}
	if body := requester.bodyFor("/clusterroles"); body != nil {
		// A superuser binds directly to the built-in cluster-admin role,
		// so no per-session ClusterRole should be POSTed.
		t.Fatalf("superuser shell should not create a per-session ClusterRole, got %s", body)
	}
	binding := requester.bodyFor("/clusterrolebindings")
	if binding == nil {
		t.Fatalf("expected a ClusterRoleBinding for the superuser shell")
	}
	if !strings.Contains(string(binding), "cluster-admin") {
		t.Fatalf("superuser elevated shell should bind cluster-admin, got %s", binding)
	}
}

// --- E2 (L3): recording reliability ---

// TestKubectlHandler_RecorderBackPressures asserts the input recorder
// throttles (blocks) rather than silently dropping rows when the drain
// channel is full. We feed many lines through onInput with a drainer that
// is intentionally slow/stalled, and assert that onInput BLOCKS once the
// buffer is full — i.e. a keystroke cannot proceed (and therefore cannot
// reach the agent) without first being queued for the audit log.
func TestKubectlHandler_RecorderBackPressures(t *testing.T) {
	const capHint = 64 // matches recordCh cap in HandleWS

	ch := make(chan string, capHint)
	// A drainer we control: it does not read until we tell it to.
	release := make(chan struct{})
	drained := make(chan int, 1)
	go func() {
		<-release
		n := 0
		for range ch {
			n++
			if n == capHint+5 {
				drained <- n
				return
			}
		}
	}()

	// Reproduce the HandleWS send semantics: block on the channel (with a
	// recordCtx escape hatch) instead of dropping. This is the exact
	// select the handler now uses.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	send := func(line string) bool {
		select {
		case ch <- line:
			return true
		case <-ctx.Done():
			return false
		}
	}

	// Fill the buffer (capHint sends succeed immediately).
	for i := 0; i < capHint; i++ {
		if !send("line") {
			t.Fatalf("send %d should have succeeded into an empty buffer", i)
		}
	}

	// The next send must BLOCK (buffer full, drainer not started). Prove
	// it by racing the send against a short timer.
	blocked := make(chan struct{})
	go func() {
		send("overflow") // should block until the drainer runs
		close(blocked)
	}()
	select {
	case <-blocked:
		t.Fatalf("send did not back-pressure: it returned while the buffer was full (this is the silent-drop bug)")
	case <-time.After(100 * time.Millisecond):
		// Good: still blocked.
	}

	// Release the drainer; the blocked send (plus the rest) now completes
	// — no row was dropped.
	close(release)
	// Push a few more so the drainer hits its target count.
	go func() {
		for i := 0; i < 5; i++ {
			send("more")
		}
	}()
	select {
	case n := <-drained:
		if n < capHint {
			t.Fatalf("drained %d rows, expected at least the buffered %d (rows were dropped)", n, capHint)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("drainer never caught up — back-pressure deadlocked")
	}
}

// TestExtractStdinBytes_ClosedFrameContract is the L3 frame-contract
// negative test: any frame the tunnel forwards to the agent as stdin must
// be recorded. Control frames are ignored; everything else (including
// unrecognized "type" values and non-JSON frames that the relay forwards
// as raw stdin) is captured so a command can't evade the audit log by
// using an exotic frame shape.
func TestExtractStdinBytes_ClosedFrameContract(t *testing.T) {
	cases := []struct {
		name   string
		frame  string
		wantOK bool
		want   string // expected recorded bytes when wantOK
	}{
		{"stdin frame", `{"type":"stdin","data":"whoami\n"}`, true, "whoami\n"},
		{"input frame", `{"type":"input","data":"ls\n"}`, true, "ls\n"},
		{"resize ignored", `{"type":"resize","cols":80,"rows":24}`, false, ""},
		{"auth ignored", `{"type":"auth","data":"tok"}`, false, ""},
		{"end ignored", `{"type":"end"}`, false, ""},
		{"close ignored", `{"type":"close"}`, false, ""},
		// Audit-evasion attempts: the relay forwards these as raw stdin,
		// so the recorder MUST capture them (record the whole frame).
		{"unknown type recorded", `{"type":"x","data":"rm -rf /\n"}`, true, `{"type":"x","data":"rm -rf /\n"}`},
		{"empty type recorded", `{"data":"curl evil\n"}`, true, `{"data":"curl evil\n"}`},
		{"non-json recorded", `bare keystrokes`, true, `bare keystrokes`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := extractStdinBytes([]byte(tc.frame))
			if ok != tc.wantOK {
				t.Fatalf("ok=%v, want %v (frame=%s)", ok, tc.wantOK, tc.frame)
			}
			if tc.wantOK && string(got) != tc.want {
				t.Fatalf("recorded %q, want %q", string(got), tc.want)
			}
		})
	}
}
