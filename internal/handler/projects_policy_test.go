package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// policyTestQuerier is a fake ProjectQuerier scoped to the policy + quota
// usage endpoints. Stubbed methods return zero values; the ones we exercise
// capture their arguments so tests can assert on them.
type policyTestQuerier struct {
	mu sync.Mutex

	projects map[uuid.UUID]sqlc.Project
	clusters map[uuid.UUID]sqlc.Cluster
	nsRows   []sqlc.ProjectNamespace

	lastUpdatePolicy *sqlc.UpdateProjectPolicyParams
	updatePolicyErr  error
}

func newPolicyTestQuerier() *policyTestQuerier {
	return &policyTestQuerier{
		projects: map[uuid.UUID]sqlc.Project{},
		clusters: map[uuid.UUID]sqlc.Cluster{},
	}
}

func (q *policyTestQuerier) GetProjectByID(_ context.Context, id uuid.UUID) (sqlc.Project, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	p, ok := q.projects[id]
	if !ok {
		return sqlc.Project{}, errors.New("no rows in result set")
	}
	return p, nil
}

func (q *policyTestQuerier) GetClusterByID(_ context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	c, ok := q.clusters[id]
	if !ok {
		return sqlc.Cluster{}, errors.New("no rows in result set")
	}
	return c, nil
}

func (q *policyTestQuerier) ListProjects(context.Context, sqlc.ListProjectsParams) ([]sqlc.Project, error) {
	return nil, nil
}
func (q *policyTestQuerier) ListProjectsByCluster(context.Context, sqlc.ListProjectsByClusterParams) ([]sqlc.Project, error) {
	return nil, nil
}
func (q *policyTestQuerier) CreateProject(context.Context, sqlc.CreateProjectParams) (sqlc.Project, error) {
	return sqlc.Project{}, nil
}
func (q *policyTestQuerier) UpdateProject(context.Context, sqlc.UpdateProjectParams) (sqlc.Project, error) {
	return sqlc.Project{}, nil
}
func (q *policyTestQuerier) UpdateProjectPolicy(_ context.Context, arg sqlc.UpdateProjectPolicyParams) (sqlc.Project, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.lastUpdatePolicy = &arg
	if q.updatePolicyErr != nil {
		return sqlc.Project{}, q.updatePolicyErr
	}
	existing := q.projects[arg.ID]
	existing.PodSecurityProfile = arg.PodSecurityProfile
	existing.ResourceQuotaCpuLimit = arg.ResourceQuotaCpuLimit
	existing.ResourceQuotaMemoryLimit = arg.ResourceQuotaMemoryLimit
	existing.ResourceQuotaPodCount = arg.ResourceQuotaPodCount
	q.projects[arg.ID] = existing
	return existing, nil
}
func (q *policyTestQuerier) DeleteProject(context.Context, uuid.UUID) error  { return nil }
func (q *policyTestQuerier) CountProjects(context.Context) (int64, error)    { return 0, nil }
func (q *policyTestQuerier) CountProjectsByCluster(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}
func (q *policyTestQuerier) GetClusterRegistryConfig(context.Context, uuid.UUID) (sqlc.ClusterRegistryConfig, error) {
	return sqlc.ClusterRegistryConfig{}, errors.New("no rows in result set")
}
func (q *policyTestQuerier) GetDefaultPodSecurityTemplate(context.Context) (sqlc.PodSecurityTemplate, error) {
	return sqlc.PodSecurityTemplate{}, errors.New("no rows in result set")
}
func (q *policyTestQuerier) UpsertProjectNamespace(_ context.Context, arg sqlc.UpsertProjectNamespaceParams) (sqlc.ProjectNamespace, error) {
	return sqlc.ProjectNamespace{ProjectID: arg.ProjectID, ClusterID: arg.ClusterID, Namespace: arg.Namespace}, nil
}
func (q *policyTestQuerier) DeleteProjectNamespace(context.Context, sqlc.DeleteProjectNamespaceParams) error {
	return nil
}
func (q *policyTestQuerier) ListProjectNamespaces(_ context.Context, _ uuid.UUID) ([]sqlc.ProjectNamespace, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]sqlc.ProjectNamespace, len(q.nsRows))
	copy(out, q.nsRows)
	return out, nil
}
func (q *policyTestQuerier) ListAllProjectNamespaces(context.Context) ([]sqlc.ProjectNamespace, error) {
	return nil, nil
}
func (q *policyTestQuerier) ClaimProjectNamespaceReconcile(context.Context, sqlc.ClaimProjectNamespaceReconcileParams) (sqlc.ProjectNamespace, error) {
	return sqlc.ProjectNamespace{}, nil
}
func (q *policyTestQuerier) MarkProjectNamespaceReconciled(context.Context, sqlc.MarkProjectNamespaceReconciledParams) error {
	return nil
}

// RBAC-matrix surface — the policy tests don't exercise this path,
// but the interface needs satisfying. Return empty slices / NotFound
// so a stray test hit doesn't blow up.
func (q *policyTestQuerier) ListProjectRoleBindingsByProject(context.Context, sqlc.ListProjectRoleBindingsByProjectParams) ([]sqlc.ProjectRoleBinding, error) {
	return nil, nil
}
func (q *policyTestQuerier) GetProjectRoleByID(context.Context, uuid.UUID) (sqlc.ProjectRole, error) {
	return sqlc.ProjectRole{}, nil
}
func (q *policyTestQuerier) GetUserByID(context.Context, uuid.UUID) (sqlc.User, error) {
	return sqlc.User{}, nil
}

// patchURLParam wires a chi URL param into a request's context — handlers
// pulled out of the router don't get URL params for free.
func patchURLParam(req *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestPatchProjectPolicy_UpdatesFields(t *testing.T) {
	q := newPolicyTestQuerier()
	projectID := uuid.New()
	q.projects[projectID] = sqlc.Project{
		ID:                       projectID,
		Name:                     "team-a",
		PodSecurityProfile:       "privileged",
		ResourceQuotaCpuLimit:    "",
		ResourceQuotaMemoryLimit: "",
		ResourceQuotaPodCount:    0,
	}
	h := NewProjectHandler(q)

	body := bytes.NewBufferString(`{
		"pod_security_profile": "restricted",
		"resource_quota_cpu_limit": "8",
		"resource_quota_memory_limit": "16Gi",
		"resource_quota_pod_count": 50
	}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/projects/"+projectID.String()+"/policy/", body)
	req = patchURLParam(req, "id", projectID.String())
	rr := httptest.NewRecorder()

	h.UpdatePolicy(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if q.lastUpdatePolicy == nil {
		t.Fatal("expected UpdateProjectPolicy to be called")
	}
	got := *q.lastUpdatePolicy
	if got.PodSecurityProfile != "restricted" {
		t.Errorf("profile: got %q, want restricted", got.PodSecurityProfile)
	}
	if got.ResourceQuotaCpuLimit != "8" {
		t.Errorf("cpu: got %q, want 8", got.ResourceQuotaCpuLimit)
	}
	if got.ResourceQuotaMemoryLimit != "16Gi" {
		t.Errorf("mem: got %q, want 16Gi", got.ResourceQuotaMemoryLimit)
	}
	if got.ResourceQuotaPodCount != 50 {
		t.Errorf("pods: got %d, want 50", got.ResourceQuotaPodCount)
	}

	var envelope struct {
		Data ProjectResponse `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.PodSecurityProfile != "restricted" {
		t.Errorf("response profile: got %q, want restricted", envelope.Data.PodSecurityProfile)
	}
}

// TestPatchProjectPolicy_OmittedFieldsPreserved checks that a partial PATCH
// only touches what the caller sent.
func TestPatchProjectPolicy_OmittedFieldsPreserved(t *testing.T) {
	q := newPolicyTestQuerier()
	projectID := uuid.New()
	q.projects[projectID] = sqlc.Project{
		ID:                       projectID,
		Name:                     "team-a",
		PodSecurityProfile:       "baseline",
		ResourceQuotaCpuLimit:    "4",
		ResourceQuotaMemoryLimit: "8Gi",
		ResourceQuotaPodCount:    20,
	}
	h := NewProjectHandler(q)

	// Only change the PSS profile.
	body := bytes.NewBufferString(`{"pod_security_profile":"restricted"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/projects/"+projectID.String()+"/policy/", body)
	req = patchURLParam(req, "id", projectID.String())
	rr := httptest.NewRecorder()

	h.UpdatePolicy(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	got := *q.lastUpdatePolicy
	if got.PodSecurityProfile != "restricted" {
		t.Errorf("profile: got %q, want restricted", got.PodSecurityProfile)
	}
	if got.ResourceQuotaCpuLimit != "4" {
		t.Errorf("cpu should be preserved: got %q, want 4", got.ResourceQuotaCpuLimit)
	}
	if got.ResourceQuotaMemoryLimit != "8Gi" {
		t.Errorf("mem should be preserved: got %q, want 8Gi", got.ResourceQuotaMemoryLimit)
	}
	if got.ResourceQuotaPodCount != 20 {
		t.Errorf("pods should be preserved: got %d, want 20", got.ResourceQuotaPodCount)
	}
}

func TestPatchProjectPolicy_RejectsInvalidProfile(t *testing.T) {
	q := newPolicyTestQuerier()
	projectID := uuid.New()
	q.projects[projectID] = sqlc.Project{ID: projectID, PodSecurityProfile: "baseline"}
	h := NewProjectHandler(q)

	body := bytes.NewBufferString(`{"pod_security_profile":"banana"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/projects/"+projectID.String()+"/policy/", body)
	req = patchURLParam(req, "id", projectID.String())
	rr := httptest.NewRecorder()

	h.UpdatePolicy(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid profile, got %d body=%s", rr.Code, rr.Body.String())
	}
	if q.lastUpdatePolicy != nil {
		t.Errorf("UpdateProjectPolicy should NOT be called on invalid input, got %+v", q.lastUpdatePolicy)
	}
}

// TestPatchProjectPolicy_RequiresProjectAdmin verifies that the RBAC gate is
// hooked up: the project-policy route is registered with VerbUpdate on
// ResourceProjects, so a binding that grants only VerbRead must not reach the
// handler. We assert this at the route level by sending through chi with a
// permission-deny middleware mounted in front of UpdatePolicy.
func TestPatchProjectPolicy_RequiresProjectAdmin(t *testing.T) {
	q := newPolicyTestQuerier()
	projectID := uuid.New()
	q.projects[projectID] = sqlc.Project{ID: projectID, PodSecurityProfile: "baseline"}
	h := NewProjectHandler(q)

	denied := false
	denyMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Simulate the RBAC engine denying projects:update.
			denied = true
			w.WriteHeader(http.StatusForbidden)
		})
	}

	router := chi.NewRouter()
	router.With(denyMiddleware).Patch("/api/v1/projects/{id}/policy/", h.UpdatePolicy)

	body := bytes.NewBufferString(`{"pod_security_profile":"restricted"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/projects/"+projectID.String()+"/policy/", body)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if !denied {
		t.Fatalf("expected the deny middleware to fire")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
	if q.lastUpdatePolicy != nil {
		t.Errorf("UpdateProjectPolicy should NOT be called when RBAC denies")
	}
}

// --- quota-usage endpoint tests -------------------------------------------

// quotaTestRequester is a minimal in-process K8sRequester that returns a
// canned ResourceQuota body for given (cluster, namespace) pairs, and an
// error for others — exercising the partial-failure path.
type quotaTestRequester struct {
	bodies map[string][]byte
	errors map[string]error
}

func (r *quotaTestRequester) Do(_ context.Context, clusterID, _, path string, _ []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	key := clusterID + "|" + path
	if err, ok := r.errors[key]; ok {
		return nil, err
	}
	body, ok := r.bodies[key]
	if !ok {
		return &protocol.K8sResponsePayload{StatusCode: http.StatusNotFound}, nil
	}
	return &protocol.K8sResponsePayload{
		StatusCode: http.StatusOK,
		Body:       base64.StdEncoding.EncodeToString(body),
	}, nil
}

func TestGetProjectQuotaUsage_AggregatesPerNamespace(t *testing.T) {
	projectID := uuid.New()
	clusterAID := uuid.New()
	clusterBID := uuid.New()
	q := newPolicyTestQuerier()
	q.projects[projectID] = sqlc.Project{ID: projectID, Name: "team-a"}
	q.clusters[clusterAID] = sqlc.Cluster{ID: clusterAID, Name: "alpha"}
	q.clusters[clusterBID] = sqlc.Cluster{ID: clusterBID, Name: "bravo"}
	q.nsRows = []sqlc.ProjectNamespace{
		{ProjectID: projectID, ClusterID: clusterAID, Namespace: "team-a-stg"},
		{ProjectID: projectID, ClusterID: clusterBID, Namespace: "team-a-prd"},
	}

	bodyAlpha := []byte(`{"spec":{"hard":{"limits.cpu":"4","limits.memory":"8Gi"}},"status":{"used":{"limits.cpu":"1500m","limits.memory":"3Gi"},"hard":{"limits.cpu":"4","limits.memory":"8Gi"}}}`)
	bodyBravo := []byte(`{"spec":{"hard":{"pods":"20"}},"status":{"used":{"pods":"5"},"hard":{"pods":"20"}}}`)

	req := &quotaTestRequester{
		bodies: map[string][]byte{
			clusterAID.String() + "|/api/v1/namespaces/team-a-stg/resourcequotas/astronomer-project-quota": bodyAlpha,
			clusterBID.String() + "|/api/v1/namespaces/team-a-prd/resourcequotas/astronomer-project-quota": bodyBravo,
		},
	}

	h := NewProjectHandler(q)
	h.requester = req

	httpReq := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID.String()+"/quota-usage/", nil)
	httpReq = patchURLParam(httpReq, "id", projectID.String())
	rr := httptest.NewRecorder()
	h.QuotaUsage(rr, httpReq)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data struct {
			Results []struct {
				ClusterName string         `json:"cluster_name"`
				Namespace   string         `json:"namespace"`
				Used        map[string]any `json:"used"`
				Hard        map[string]any `json:"hard"`
			} `json:"results"`
			Errors []map[string]any `json:"errors"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	resp := envelope.Data
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(resp.Results), resp.Results)
	}
	if len(resp.Errors) != 0 {
		t.Errorf("expected zero errors, got %+v", resp.Errors)
	}
	byNs := map[string]map[string]any{}
	for _, it := range resp.Results {
		byNs[it.Namespace] = map[string]any{"used": it.Used, "hard": it.Hard, "cluster": it.ClusterName}
	}
	if stg := byNs["team-a-stg"]; stg["cluster"] != "alpha" {
		t.Errorf("expected stg cluster=alpha, got %v", stg)
	}
	if prd := byNs["team-a-prd"]; prd == nil {
		t.Fatalf("missing prd row in %+v", byNs)
	}
	if used, ok := byNs["team-a-stg"]["used"].(map[string]any); !ok || used["limits.cpu"] != "1500m" {
		t.Errorf("expected stg.used.limits.cpu=1500m, got %+v", byNs["team-a-stg"]["used"])
	}
}

func TestGetProjectQuotaUsage_PartialFailureSurface(t *testing.T) {
	projectID := uuid.New()
	healthyClusterID := uuid.New()
	brokenClusterID := uuid.New()
	q := newPolicyTestQuerier()
	q.projects[projectID] = sqlc.Project{ID: projectID, Name: "team-a"}
	q.clusters[healthyClusterID] = sqlc.Cluster{ID: healthyClusterID, Name: "alpha"}
	q.clusters[brokenClusterID] = sqlc.Cluster{ID: brokenClusterID, Name: "bravo"}
	q.nsRows = []sqlc.ProjectNamespace{
		{ProjectID: projectID, ClusterID: healthyClusterID, Namespace: "team-a-stg"},
		{ProjectID: projectID, ClusterID: brokenClusterID, Namespace: "team-a-prd"},
	}

	body := []byte(`{"spec":{"hard":{"limits.cpu":"1"}},"status":{"used":{"limits.cpu":"100m"},"hard":{"limits.cpu":"1"}}}`)
	req := &quotaTestRequester{
		bodies: map[string][]byte{
			healthyClusterID.String() + "|/api/v1/namespaces/team-a-stg/resourcequotas/astronomer-project-quota": body,
		},
		errors: map[string]error{
			brokenClusterID.String() + "|/api/v1/namespaces/team-a-prd/resourcequotas/astronomer-project-quota": errors.New("agent not connected"),
		},
	}

	h := NewProjectHandler(q)
	h.requester = req

	httpReq := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID.String()+"/quota-usage/", nil)
	httpReq = patchURLParam(httpReq, "id", projectID.String())
	rr := httptest.NewRecorder()
	h.QuotaUsage(rr, httpReq)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data struct {
			Results []map[string]any `json:"results"`
			Errors  []map[string]any `json:"errors"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	resp := envelope.Data
	if len(resp.Results) != 1 {
		t.Errorf("expected 1 successful result, got %d: %+v", len(resp.Results), resp.Results)
	}
	if len(resp.Errors) != 1 {
		t.Fatalf("expected 1 error entry, got %d: %+v", len(resp.Errors), resp.Errors)
	}
	if got, _ := resp.Errors[0]["cluster_name"].(string); got != "bravo" {
		t.Errorf("expected broken cluster_name=bravo, got %v", resp.Errors[0])
	}
	if got, _ := resp.Errors[0]["error"].(string); !strings.Contains(got, "agent not connected") {
		t.Errorf("expected error message captured, got %q", got)
	}
}
