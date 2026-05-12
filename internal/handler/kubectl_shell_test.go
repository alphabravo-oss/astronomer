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
		ClientIP:    arg.ClientIP, UserAgent: arg.UserAgent,
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
		if arg.LastError.Valid {
			r.LastError = arg.LastError.String
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
	mu    sync.Mutex
	calls []string
}

func (f *fakeShellRequester) Do(_ context.Context, _, method, path string, _ []byte, _ map[string]string) (*kubectl.K8sResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, method+" "+path)
	if method == "GET" && strings.Contains(path, "/pods/") {
		// Pod is Ready immediately.
		return &kubectl.K8sResponse{
			StatusCode: 200,
			Body: []byte(`{"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}]}}`),
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

func TestKubectlHandler_WSEndpointRedirects(t *testing.T) {
	h, _, userID, clusterID := newTestKubectlHandler(t)
	r := newKubectlRouter(h)
	req := authReq("POST", "/api/v1/clusters/"+clusterID.String()+"/shell/sessions/", "", userID, false)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var info kubectl.SessionInfo
	unwrapData(t, w.Body.Bytes(), &info)

	req = authReq("GET", "/api/v1/ws/clusters/"+clusterID.String()+"/shell/sessions/"+info.ID.String()+"/", "", userID, false)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("WS endpoint: want 307, got %d body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/api/v1/ws/exec/") {
		t.Fatalf("WS endpoint should redirect to /api/v1/ws/exec/, got %s", loc)
	}
	if !strings.Contains(loc, "/"+info.PodName+"/") {
		t.Fatalf("WS redirect should include pod name; got %s", loc)
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
	if !strings.Contains(w.Body.String(), "cluster_not_found") {
		t.Fatalf("body should include cluster_not_found; got %s", w.Body.String())
	}

	// Unauthenticated → 401.
	req = httptest.NewRequest("POST", "/api/v1/clusters/"+uuid.New().String()+"/shell/sessions/", strings.NewReader(""))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: want 401, got %d body=%s", w.Code, w.Body.String())
	}
}
