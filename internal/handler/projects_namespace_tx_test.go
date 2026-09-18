package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

// fakeProjectTxStore is the committed backing store the transactional
// Add/RemoveNamespace path writes to. runTx applies scratch mutations only when
// the closure returns nil, modelling real transaction atomicity so a mid-tx
// failure leaves the JSONB list and the sidecar untouched.
type fakeProjectTxStore struct {
	mu        sync.Mutex
	project   sqlc.Project
	nsRows    map[string]bool
	upsertErr error
	deleteErr error
	auditErr  error
	taskErr   error
	audits    []sqlc.AuditOutbox
	tasks     []sqlc.UpsertTaskOutboxParams
}

func newFakeProjectTxStore(p sqlc.Project, namespaces ...string) *fakeProjectTxStore {
	rows := map[string]bool{}
	for _, ns := range namespaces {
		rows[ns] = true
	}
	return &fakeProjectTxStore{project: p, nsRows: rows}
}

func (s *fakeProjectTxStore) runTx() projectRunTxFunc {
	return func(ctx context.Context, fn func(q ProjectNamespaceTx) error) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		scratchRows := make(map[string]bool, len(s.nsRows))
		for k, v := range s.nsRows {
			scratchRows[k] = v
		}
		scratchAudits := append([]sqlc.AuditOutbox(nil), s.audits...)
		scratchTasks := append([]sqlc.UpsertTaskOutboxParams(nil), s.tasks...)
		scratch := &fakeProjectTx{store: s, project: s.project, nsRows: scratchRows, audits: scratchAudits, tasks: scratchTasks}
		if err := fn(scratch); err != nil {
			return err // rollback: discard scratch
		}
		s.project = scratch.project
		s.nsRows = scratch.nsRows
		s.audits = scratch.audits
		s.tasks = scratch.tasks
		return nil
	}
}

type fakeProjectTx struct {
	store   *fakeProjectTxStore
	project sqlc.Project
	nsRows  map[string]bool
	audits  []sqlc.AuditOutbox
	tasks   []sqlc.UpsertTaskOutboxParams
}

func (t *fakeProjectTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if t.store.auditErr != nil {
		return sqlc.AuditOutbox{}, t.store.auditErr
	}
	for _, existing := range t.audits {
		if existing.DedupeKey == arg.DedupeKey {
			return existing, nil
		}
	}
	row := sqlc.AuditOutbox{
		ID: arg.ID, DedupeKey: arg.DedupeKey, EventCreatedAt: arg.EventCreatedAt,
		Action: arg.Action, ResourceType: arg.ResourceType, ResourceID: arg.ResourceID,
		ResourceName: arg.ResourceName, Detail: arg.Detail, Status: "pending",
	}
	t.audits = append(t.audits, row)
	return row, nil
}

func (t *fakeProjectTx) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	if t.store.taskErr != nil {
		return sqlc.TaskOutbox{}, t.store.taskErr
	}
	for _, existing := range t.tasks {
		if existing.DedupeKey == arg.DedupeKey {
			return sqlc.TaskOutbox{DedupeKey: existing.DedupeKey, TaskType: existing.TaskType, Payload: existing.Payload, QueueName: existing.QueueName}, nil
		}
	}
	t.tasks = append(t.tasks, arg)
	return sqlc.TaskOutbox{ID: uuid.New(), DedupeKey: arg.DedupeKey, TaskType: arg.TaskType, Payload: arg.Payload, QueueName: arg.QueueName}, nil
}

func (t *fakeProjectTx) GetProjectByIDForUpdate(_ context.Context, _ uuid.UUID) (sqlc.Project, error) {
	return t.project, nil
}

func (t *fakeProjectTx) UpdateProject(_ context.Context, arg sqlc.UpdateProjectParams) (sqlc.Project, error) {
	t.project.Namespaces = arg.Namespaces
	t.project.DisplayName = arg.DisplayName
	t.project.Description = arg.Description
	t.project.ResourceQuota = arg.ResourceQuota
	t.project.LimitRange = arg.LimitRange
	t.project.NetworkPolicyMode = arg.NetworkPolicyMode
	t.project.PodSecurityProfile = arg.PodSecurityProfile
	return t.project, nil
}

func (t *fakeProjectTx) UpsertProjectNamespace(_ context.Context, arg sqlc.UpsertProjectNamespaceParams) (sqlc.ProjectNamespace, error) {
	if t.store.upsertErr != nil {
		return sqlc.ProjectNamespace{}, t.store.upsertErr
	}
	t.nsRows[arg.Namespace] = true
	return sqlc.ProjectNamespace{ProjectID: arg.ProjectID, ClusterID: arg.ClusterID, Namespace: arg.Namespace}, nil
}

func (t *fakeProjectTx) DeleteProjectNamespace(_ context.Context, arg sqlc.DeleteProjectNamespaceParams) error {
	if t.store.deleteErr != nil {
		return t.store.deleteErr
	}
	delete(t.nsRows, arg.Namespace)
	return nil
}

type fakeRBACInvalidator struct {
	invalidateAll int
	invalidate    int
}

func (f *fakeRBACInvalidator) GetUserBindings(context.Context, string) ([]rbac.RoleBinding, error) {
	return nil, nil
}
func (f *fakeRBACInvalidator) Invalidate(string) { f.invalidate++ }
func (f *fakeRBACInvalidator) InvalidateAll()    { f.invalidateAll++ }

func seedTxProject(q *policyTestQuerier, clusterID uuid.UUID, namespaces []string) (uuid.UUID, sqlc.Project) {
	id := uuid.New()
	nsJSON, _ := json.Marshal(namespaces)
	p := sqlc.Project{
		ID:                id,
		Name:              "team-a",
		DisplayName:       "Team A",
		ClusterID:         clusterID,
		Namespaces:        nsJSON,
		ResourceQuota:     json.RawMessage(`{}`),
		LimitRange:        json.RawMessage(`{}`),
		NetworkPolicyMode: "none",
	}
	q.projects[id] = p
	return id, p
}

// TestAddNamespace_TxRollsBackOnSidecarFailure verifies the JSONB update and the
// project_namespaces sidecar write are atomic: when the sidecar upsert fails the
// whole transaction rolls back and the request fails, rather than the old
// behaviour of committing the JSONB and silently swallowing the sidecar error
// while returning 200.
func TestAddNamespace_TxRollsBackOnSidecarFailure(t *testing.T) {
	q := newPolicyTestQuerier()
	callerID := uuid.New()
	clusterID := uuid.New()
	id, p := seedTxProject(q, clusterID, []string{})

	store := newFakeProjectTxStore(p)
	store.upsertErr = errors.New("sidecar write failed")

	h := NewProjectHandler(q)
	h.SetRunTx(store.runTx())
	grantClusterNamespaceAssignment(h, clusterID)

	req := authedProjectRequest(t, http.MethodPost, "/api/v1/projects/"+id.String()+"/add-namespace/", callerID, map[string]any{"namespace": "payments"})
	req = patchURLParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.AddNamespace(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s, want 500", rec.Code, rec.Body.String())
	}
	// JSONB must be unchanged — the transaction rolled back.
	if got := decodeNamespaceList(store.project.Namespaces); len(got) != 0 {
		t.Fatalf("project namespaces = %v, want empty (rolled back)", got)
	}
	if store.nsRows["payments"] {
		t.Fatalf("sidecar row for payments present, want absent (rolled back)")
	}
}

// TestAddNamespace_TxCommitsBothHalves verifies the happy path writes both the
// JSONB list and the sidecar row atomically and returns 200.
func TestAddNamespace_TxCommitsBothHalves(t *testing.T) {
	q := newPolicyTestQuerier()
	callerID := uuid.New()
	clusterID := uuid.New()
	id, p := seedTxProject(q, clusterID, []string{})

	store := newFakeProjectTxStore(p)
	h := NewProjectHandler(q)
	h.SetRunTx(store.runTx())
	grantClusterNamespaceAssignment(h, clusterID)

	req := authedProjectRequest(t, http.MethodPost, "/api/v1/projects/"+id.String()+"/add-namespace/", callerID, map[string]any{"namespace": "payments"})
	req = patchURLParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.AddNamespace(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	got := decodeNamespaceList(store.project.Namespaces)
	if len(got) != 1 || got[0] != "payments" {
		t.Fatalf("project namespaces = %v, want [payments]", got)
	}
	if !store.nsRows["payments"] {
		t.Fatalf("sidecar row for payments absent, want present")
	}
	if len(store.audits) != 1 || store.audits[0].Action != "project.add_namespace" {
		t.Fatalf("transactional audit rows = %#v", store.audits)
	}
	if len(store.tasks) != 1 {
		t.Fatalf("transactional task rows = %d, want 1", len(store.tasks))
	}
}

func TestAddNamespace_RebalancesEveryNamespaceImmediately(t *testing.T) {
	q := newPolicyTestQuerier()
	callerID, clusterID := uuid.New(), uuid.New()
	id, p := seedTxProject(q, clusterID, []string{"alpha"})
	p.ResourceQuotaCpuLimit = "1"
	p.ResourceQuotaMemoryLimit = "2"
	p.ResourceQuotaPodCount = 2
	q.projects[id] = p

	store := newFakeProjectTxStore(p, "alpha")
	h := NewProjectHandler(q)
	h.SetRunTx(store.runTx())
	grantClusterNamespaceAssignment(h, clusterID)

	req := authedProjectRequest(t, http.MethodPost, "/api/v1/projects/"+id.String()+"/add-namespace/", callerID, map[string]any{"namespace": "beta"})
	req = patchURLParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.AddNamespace(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	got := map[string]bool{}
	for _, row := range store.tasks {
		var payload tasks.ProjectReconcilePayload
		if err := json.Unmarshal(row.Payload, &payload); err != nil {
			t.Fatalf("decode task payload: %v", err)
		}
		got[payload.Namespace] = true
	}
	if !got["alpha"] || !got["beta"] {
		t.Fatalf("reconcile tasks = %v, want alpha and beta", got)
	}
}

func TestAddNamespace_TxRollsBackWhenAuditIntentFails(t *testing.T) {
	q := newPolicyTestQuerier()
	callerID := uuid.New()
	clusterID := uuid.New()
	id, p := seedTxProject(q, clusterID, []string{})

	store := newFakeProjectTxStore(p)
	store.auditErr = errors.New("audit outbox unavailable")
	h := NewProjectHandler(q)
	h.SetRunTx(store.runTx())
	grantClusterNamespaceAssignment(h, clusterID)

	req := authedProjectRequest(t, http.MethodPost, "/api/v1/projects/"+id.String()+"/add-namespace/", callerID, map[string]any{"namespace": "payments"})
	req = patchURLParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.AddNamespace(rec, req)

	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"audit_unavailable"`) {
		t.Fatalf("status=%d body=%s, want 503 audit_unavailable", rec.Code, rec.Body.String())
	}
	if got := decodeNamespaceList(store.project.Namespaces); len(got) != 0 || store.nsRows["payments"] || len(store.audits) != 0 {
		t.Fatalf("rollback state namespaces=%v sidecar=%v audits=%d", got, store.nsRows["payments"], len(store.audits))
	}
}

func TestAddNamespace_TxRollsBackWhenTaskIntentFails(t *testing.T) {
	q := newPolicyTestQuerier()
	callerID, clusterID := uuid.New(), uuid.New()
	id, p := seedTxProject(q, clusterID, []string{"alpha"})
	store := newFakeProjectTxStore(p, "alpha")
	store.taskErr = errors.New("task outbox unavailable")
	h := NewProjectHandler(q)
	h.SetRunTx(store.runTx())
	grantClusterNamespaceAssignment(h, clusterID)

	req := authedProjectRequest(t, http.MethodPost, "/api/v1/projects/"+id.String()+"/add-namespace/", callerID, map[string]any{"namespace": "beta"})
	req = patchURLParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.AddNamespace(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s, want 500", rec.Code, rec.Body.String())
	}
	if got := decodeNamespaceList(store.project.Namespaces); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("project namespaces = %v, want [alpha] after rollback", got)
	}
	if store.nsRows["beta"] || len(store.tasks) != 0 || len(store.audits) != 0 {
		t.Fatalf("rollback state sidecar=%v tasks=%d audits=%d", store.nsRows["beta"], len(store.tasks), len(store.audits))
	}
}

func TestRemoveNamespace_CommitsCleanupAndRebalanceIntents(t *testing.T) {
	q := newPolicyTestQuerier()
	callerID, clusterID := uuid.New(), uuid.New()
	id, p := seedTxProject(q, clusterID, []string{"alpha", "beta"})
	store := newFakeProjectTxStore(p, "alpha", "beta")
	h := NewProjectHandler(q)
	h.SetRunTx(store.runTx())

	req := authedProjectRequest(t, http.MethodPost, "/api/v1/projects/"+id.String()+"/remove-namespace/", callerID, map[string]any{"namespace": "beta"})
	req = patchURLParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.RemoveNamespace(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if store.nsRows["beta"] {
		t.Fatal("removed namespace sidecar still exists")
	}
	if len(store.tasks) != 2 || len(store.audits) != 1 {
		t.Fatalf("transaction writes tasks=%d audits=%d, want 2/1", len(store.tasks), len(store.audits))
	}
	seen := map[string]string{}
	for _, row := range store.tasks {
		var payload tasks.ProjectReconcilePayload
		if err := json.Unmarshal(row.Payload, &payload); err != nil {
			t.Fatalf("decode task payload: %v", err)
		}
		seen[payload.Namespace] = payload.Op
	}
	if seen["beta"] != "remove" || seen["alpha"] != "apply" {
		t.Fatalf("task intents = %#v, want beta/remove and alpha/apply", seen)
	}
}

func TestRemoveNamespace_TxRollsBackWhenTaskIntentFails(t *testing.T) {
	q := newPolicyTestQuerier()
	callerID, clusterID := uuid.New(), uuid.New()
	id, p := seedTxProject(q, clusterID, []string{"alpha", "beta"})
	store := newFakeProjectTxStore(p, "alpha", "beta")
	store.taskErr = errors.New("task outbox unavailable")
	h := NewProjectHandler(q)
	h.SetRunTx(store.runTx())

	req := authedProjectRequest(t, http.MethodPost, "/api/v1/projects/"+id.String()+"/remove-namespace/", callerID, map[string]any{"namespace": "beta"})
	req = patchURLParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.RemoveNamespace(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s, want 500", rec.Code, rec.Body.String())
	}
	if got := decodeNamespaceList(store.project.Namespaces); len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("project namespaces = %v, want [alpha beta] after rollback", got)
	}
	if !store.nsRows["beta"] || len(store.tasks) != 0 || len(store.audits) != 0 {
		t.Fatalf("rollback state beta_sidecar=%v tasks=%d audits=%d", store.nsRows["beta"], len(store.tasks), len(store.audits))
	}
}

func TestNamespaceMutationWithoutTransactionRunnerFailsClosed(t *testing.T) {
	q := newPolicyTestQuerier()
	callerID, clusterID := uuid.New(), uuid.New()
	id, _ := seedTxProject(q, clusterID, nil)
	h := NewProjectHandler(q)
	grantClusterNamespaceAssignment(h, clusterID)

	req := authedProjectRequest(t, http.MethodPost, "/api/v1/projects/"+id.String()+"/add-namespace/", callerID, map[string]any{"namespace": "payments"})
	req = patchURLParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.AddNamespace(rec, req)

	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"audit_unavailable"`) {
		t.Fatalf("status=%d body=%s, want 503 audit_unavailable", rec.Code, rec.Body.String())
	}
	if got := decodeNamespaceList(q.projects[id].Namespaces); len(got) != 0 {
		t.Fatalf("project namespaces = %v, want unchanged", got)
	}
}

// TestNamespaceMutations_InvalidateRBACCache verifies AddNamespace and
// RemoveNamespace flush the namespace-scoped RBAC cache after a membership
// change so a revoked namespace stops authorizing immediately.
func TestNamespaceMutations_InvalidateRBACCache(t *testing.T) {
	q := newPolicyTestQuerier()
	callerID := uuid.New()
	clusterID := uuid.New()
	id, p := seedTxProject(q, clusterID, []string{})

	store := newFakeProjectTxStore(p)
	inv := &fakeRBACInvalidator{}
	h := NewProjectHandler(q)
	h.SetRunTx(store.runTx())
	h.SetRBACInvalidator(inv)
	grantClusterNamespaceAssignment(h, clusterID)

	addReq := authedProjectRequest(t, http.MethodPost, "/api/v1/projects/"+id.String()+"/add-namespace/", callerID, map[string]any{"namespace": "payments"})
	addReq = patchURLParam(addReq, "id", id.String())
	addRec := httptest.NewRecorder()
	h.AddNamespace(addRec, addReq)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add status=%d body=%s", addRec.Code, addRec.Body.String())
	}
	if inv.invalidateAll != 1 {
		t.Fatalf("after add: InvalidateAll called %d times, want 1", inv.invalidateAll)
	}

	// The pre-check reads GetProjectByID from q.projects — reflect the add there
	// too so RemoveNamespace's not-found pre-check passes.
	updated := q.projects[id]
	updated.Namespaces, _ = json.Marshal([]string{"payments"})
	q.projects[id] = updated

	removeReq := authedProjectRequest(t, http.MethodPost, "/api/v1/projects/"+id.String()+"/remove-namespace/", callerID, map[string]any{"namespace": "payments"})
	removeReq = patchURLParam(removeReq, "id", id.String())
	removeRec := httptest.NewRecorder()
	h.RemoveNamespace(removeRec, removeReq)
	if removeRec.Code != http.StatusOK {
		t.Fatalf("remove status=%d body=%s", removeRec.Code, removeRec.Body.String())
	}
	if inv.invalidateAll != 2 {
		t.Fatalf("after remove: InvalidateAll called %d times, want 2", inv.invalidateAll)
	}
	if store.nsRows["payments"] {
		t.Fatalf("sidecar row for payments present after remove, want absent")
	}
}
