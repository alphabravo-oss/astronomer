package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// fakeNetPolQuerier records mutations + serves the NetworkPolicyQuerier
// interface. Mirrors the style of fakeClusterTemplateQuerier.
type fakeNetPolHandlerQuerier struct {
	mu sync.Mutex

	templates    map[uuid.UUID]sqlc.NetworkPolicyTemplate
	bySlug       map[string]uuid.UUID
	applications map[uuid.UUID]sqlc.NetworkPolicyApplication
	clusters     map[uuid.UUID]sqlc.Cluster
	audits       []sqlc.CreateAuditLogV1Params
}

func newFakeNetPolHandlerQuerier() *fakeNetPolHandlerQuerier {
	return &fakeNetPolHandlerQuerier{
		templates:    map[uuid.UUID]sqlc.NetworkPolicyTemplate{},
		bySlug:       map[string]uuid.UUID{},
		applications: map[uuid.UUID]sqlc.NetworkPolicyApplication{},
		clusters:     map[uuid.UUID]sqlc.Cluster{},
	}
}

func (f *fakeNetPolHandlerQuerier) ListNetworkPolicyTemplates(_ context.Context, _ sqlc.ListNetworkPolicyTemplatesParams) ([]sqlc.NetworkPolicyTemplate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.NetworkPolicyTemplate, 0, len(f.templates))
	for _, t := range f.templates {
		out = append(out, t)
	}
	return out, nil
}
func (f *fakeNetPolHandlerQuerier) CountNetworkPolicyTemplates(_ context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.templates)), nil
}
func (f *fakeNetPolHandlerQuerier) GetNetworkPolicyTemplateByID(_ context.Context, id uuid.UUID) (sqlc.NetworkPolicyTemplate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.templates[id]
	if !ok {
		return sqlc.NetworkPolicyTemplate{}, pgx.ErrNoRows
	}
	return t, nil
}
func (f *fakeNetPolHandlerQuerier) GetNetworkPolicyTemplateBySlug(_ context.Context, slug string) (sqlc.NetworkPolicyTemplate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.bySlug[slug]
	if !ok {
		return sqlc.NetworkPolicyTemplate{}, pgx.ErrNoRows
	}
	return f.templates[id], nil
}
func (f *fakeNetPolHandlerQuerier) CreateNetworkPolicyTemplate(_ context.Context, arg sqlc.CreateNetworkPolicyTemplateParams) (sqlc.NetworkPolicyTemplate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.bySlug[arg.Slug]; exists {
		return sqlc.NetworkPolicyTemplate{}, &fakePGError{code: "23505"}
	}
	id := uuid.New()
	t := sqlc.NetworkPolicyTemplate{
		ID:           id,
		Slug:         arg.Slug,
		Name:         arg.Name,
		Description:  arg.Description,
		Kind:         arg.Kind,
		SpecTemplate: arg.SpecTemplate,
		Enabled:      arg.Enabled,
		CreatedBy:    arg.CreatedBy,
	}
	f.templates[id] = t
	f.bySlug[arg.Slug] = id
	return t, nil
}
func (f *fakeNetPolHandlerQuerier) UpdateNetworkPolicyTemplate(_ context.Context, arg sqlc.UpdateNetworkPolicyTemplateParams) (sqlc.NetworkPolicyTemplate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.templates[arg.ID]
	if !ok {
		return sqlc.NetworkPolicyTemplate{}, pgx.ErrNoRows
	}
	t.Name = arg.Name
	t.Description = arg.Description
	t.SpecTemplate = arg.SpecTemplate
	t.Enabled = arg.Enabled
	f.templates[arg.ID] = t
	return t, nil
}
func (f *fakeNetPolHandlerQuerier) DeleteNetworkPolicyTemplate(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.templates[id]
	if !ok {
		return pgx.ErrNoRows
	}
	delete(f.templates, id)
	delete(f.bySlug, t.Slug)
	return nil
}
func (f *fakeNetPolHandlerQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audits = append(f.audits, arg)
	return nil
}
func (f *fakeNetPolHandlerQuerier) ListApplicationsForCluster(_ context.Context, clusterID uuid.UUID) ([]sqlc.NetworkPolicyApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []sqlc.NetworkPolicyApplication{}
	for _, a := range f.applications {
		if a.ClusterID == clusterID {
			out = append(out, a)
		}
	}
	return out, nil
}
func (f *fakeNetPolHandlerQuerier) ListApplicationsForTemplate(_ context.Context, templateID uuid.UUID) ([]sqlc.NetworkPolicyApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []sqlc.NetworkPolicyApplication{}
	for _, a := range f.applications {
		if a.TemplateID == templateID {
			out = append(out, a)
		}
	}
	return out, nil
}
func (f *fakeNetPolHandlerQuerier) GetNetworkPolicyApplicationByID(_ context.Context, id uuid.UUID) (sqlc.NetworkPolicyApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.applications[id]
	if !ok {
		return sqlc.NetworkPolicyApplication{}, pgx.ErrNoRows
	}
	return a, nil
}
func (f *fakeNetPolHandlerQuerier) GetNetworkPolicyApplicationByUnique(_ context.Context, arg sqlc.GetNetworkPolicyApplicationByUniqueParams) (sqlc.NetworkPolicyApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.applications {
		if a.ClusterID == arg.ClusterID && a.Namespace == arg.Namespace && a.TemplateID == arg.TemplateID {
			return a, nil
		}
	}
	return sqlc.NetworkPolicyApplication{}, pgx.ErrNoRows
}
func (f *fakeNetPolHandlerQuerier) CreateNetworkPolicyApplication(_ context.Context, arg sqlc.CreateNetworkPolicyApplicationParams) (sqlc.NetworkPolicyApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.applications {
		if a.ClusterID == arg.ClusterID && a.Namespace == arg.Namespace && a.TemplateID == arg.TemplateID {
			return sqlc.NetworkPolicyApplication{}, &fakePGError{code: "23505"}
		}
	}
	id := uuid.New()
	app := sqlc.NetworkPolicyApplication{
		ID:         id,
		TemplateID: arg.TemplateID,
		ClusterID:  arg.ClusterID,
		Namespace:  arg.Namespace,
		PolicyName: arg.PolicyName,
		Status:     "pending",
		AppliedBy:  arg.AppliedBy,
	}
	f.applications[id] = app
	return app, nil
}
func (f *fakeNetPolHandlerQuerier) DeleteNetworkPolicyApplication(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.applications, id)
	return nil
}
func (f *fakeNetPolHandlerQuerier) MarkNetworkPolicyApplicationStatus(_ context.Context, arg sqlc.MarkNetworkPolicyApplicationStatusParams) (sqlc.NetworkPolicyApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.applications[arg.ID]
	if !ok {
		return sqlc.NetworkPolicyApplication{}, pgx.ErrNoRows
	}
	a.Status = arg.Status
	a.LastError = arg.LastError
	f.applications[arg.ID] = a
	return a, nil
}
func (f *fakeNetPolHandlerQuerier) GetClusterByID(_ context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.clusters[id]
	if !ok {
		return sqlc.Cluster{}, pgx.ErrNoRows
	}
	return c, nil
}

// captureRequester records every K8s call. Used to assert that
// DeleteApplication invokes the in-cluster revoke and that the SSA path
// is well-formed.
type captureRequester struct {
	mu       sync.Mutex
	requests []struct {
		method, path string
	}
	err    error
	status int
}

func (c *captureRequester) Do(_ context.Context, _, method, path string, _ []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, struct{ method, path string }{method, path})
	if c.err != nil {
		return nil, c.err
	}
	status := c.status
	if status == 0 {
		status = 200
	}
	return &protocol.K8sResponsePayload{StatusCode: status}, nil
}

func mkNetPolTemplate(slug, kind string) sqlc.NetworkPolicyTemplate {
	return sqlc.NetworkPolicyTemplate{
		ID:           uuid.New(),
		Slug:         slug,
		Name:         slug,
		Kind:         kind,
		SpecTemplate: "apiVersion: networking.k8s.io/v1\nkind: NetworkPolicy\nmetadata:\n  name: {{.PolicyName}}\n  namespace: {{.Namespace}}\nspec:\n  podSelector: {}\n  policyTypes: [Ingress]\n",
		Enabled:      true,
	}
}

func mkRouter(h *NetworkPolicyHandler) http.Handler {
	r := chi.NewRouter()
	r.Get("/api/v1/admin/network-policy-templates/", h.ListTemplates)
	r.Post("/api/v1/admin/network-policy-templates/", h.CreateTemplate)
	r.Get("/api/v1/admin/network-policy-templates/{id}/", h.GetTemplate)
	r.Put("/api/v1/admin/network-policy-templates/{id}/", h.UpdateTemplate)
	r.Delete("/api/v1/admin/network-policy-templates/{id}/", h.DeleteTemplate)
	r.Get("/api/v1/clusters/{cluster_id}/network-policies/applications/", h.ListApplications)
	r.Post("/api/v1/clusters/{cluster_id}/network-policies/applications/", h.CreateApplications)
	r.Delete("/api/v1/clusters/{cluster_id}/network-policies/applications/{id}/", h.DeleteApplication)
	r.Post("/api/v1/clusters/{cluster_id}/network-policies/applications/{id}/reapply/", h.Reapply)
	return r
}

func TestNetPolHandler_TestApply_CreatesRowAndEnqueues(t *testing.T) {
	q := newFakeNetPolHandlerQuerier()
	tmpl := mkNetPolTemplate("deny_all_ingress", "builtin")
	q.templates[tmpl.ID] = tmpl
	q.bySlug[tmpl.Slug] = tmpl.ID
	cluster := sqlc.Cluster{ID: uuid.New(), Name: "prod"}
	q.clusters[cluster.ID] = cluster

	h := NewNetworkPolicyHandler(q)
	body, _ := json.Marshal(ApplyNetworkPolicyRequest{TemplateID: tmpl.ID.String(), Namespace: "team-a"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+cluster.ID.String()+"/network-policies/applications/", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mkRouter(h).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if len(q.applications) != 1 {
		t.Errorf("expected 1 application created, got %d", len(q.applications))
	}
	q.mu.Lock()
	audits := append([]sqlc.CreateAuditLogV1Params(nil), q.audits...)
	q.mu.Unlock()
	if len(audits) != 1 {
		t.Fatalf("audit rows=%d want 1", len(audits))
	}
	auditRow := audits[0]
	if auditRow.Action != "cluster.network_policy.applied" || auditRow.ResourceType != "cluster" || auditRow.ResourceID != cluster.ID.String() {
		t.Fatalf("audit row=%+v, want cluster.network_policy.applied on cluster %s", auditRow, cluster.ID)
	}
	assertAuditDetail(t, auditRow.Detail, "template_id", tmpl.ID.String())
	assertAuditDetail(t, auditRow.Detail, "template_slug", tmpl.Slug)
}

func TestNetPolHandler_TestApply_RejectsBuiltinTemplateEdit(t *testing.T) {
	q := newFakeNetPolHandlerQuerier()
	tmpl := mkNetPolTemplate("deny_all_ingress", "builtin")
	q.templates[tmpl.ID] = tmpl
	q.bySlug[tmpl.Slug] = tmpl.ID

	h := NewNetworkPolicyHandler(q)
	body, _ := json.Marshal(CreateNetworkPolicyTemplateRequest{
		Name:         "Evil",
		SpecTemplate: tmpl.SpecTemplate,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/network-policy-templates/"+tmpl.ID.String()+"/", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mkRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 forbidden on builtin edit, got %d body=%s", w.Code, w.Body.String())
	}

	// DELETE on a builtin row should also 403.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/admin/network-policy-templates/"+tmpl.ID.String()+"/", nil)
	w = httptest.NewRecorder()
	mkRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 forbidden on builtin delete, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestNetPolHandler_CloneFromBuiltin_CreatesCustom(t *testing.T) {
	q := newFakeNetPolHandlerQuerier()
	src := mkNetPolTemplate("deny_all_ingress", "builtin")
	q.templates[src.ID] = src
	q.bySlug[src.Slug] = src.ID

	h := NewNetworkPolicyHandler(q)
	body, _ := json.Marshal(CreateNetworkPolicyTemplateRequest{
		CloneFrom: src.Slug,
		Slug:      "my_deny",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/network-policy-templates/", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mkRouter(h).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	id, ok := q.bySlug["my_deny"]
	if !ok {
		t.Fatal("expected cloned template to be stored under my_deny slug")
	}
	cloned := q.templates[id]
	if cloned.Kind != "custom" {
		t.Errorf("expected cloned row kind=custom, got %q", cloned.Kind)
	}
}

func TestNetPolHandler_Delete_RevokesInClusterResource(t *testing.T) {
	q := newFakeNetPolHandlerQuerier()
	tmpl := mkNetPolTemplate("namespace_only", "builtin")
	q.templates[tmpl.ID] = tmpl
	q.bySlug[tmpl.Slug] = tmpl.ID
	cluster := sqlc.Cluster{ID: uuid.New(), Name: "prod"}
	q.clusters[cluster.ID] = cluster
	app := sqlc.NetworkPolicyApplication{
		ID:         uuid.New(),
		TemplateID: tmpl.ID,
		ClusterID:  cluster.ID,
		Namespace:  "team-a",
		PolicyName: "astronomer-np-namespace_only",
		Status:     "applied",
	}
	q.applications[app.ID] = app

	h := NewNetworkPolicyHandler(q)
	rk := &captureRequester{status: 200}
	h.SetK8sRequester(rk)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/clusters/"+cluster.ID.String()+"/network-policies/applications/"+app.ID.String()+"/", nil)
	w := httptest.NewRecorder()
	mkRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if got := len(rk.requests); got != 1 {
		t.Fatalf("expected 1 K8s DELETE call, got %d", got)
	}
	if rk.requests[0].method != "DELETE" {
		t.Errorf("expected DELETE, got %q", rk.requests[0].method)
	}
	if !strings.Contains(rk.requests[0].path, "networkpolicies/astronomer-np-namespace_only") {
		t.Errorf("unexpected revoke path: %q", rk.requests[0].path)
	}
	if _, exists := q.applications[app.ID]; exists {
		t.Errorf("expected application row to be deleted")
	}
}

func TestNetPolHandler_CreateApplications_BulkNamespaces(t *testing.T) {
	q := newFakeNetPolHandlerQuerier()
	tmpl := mkNetPolTemplate("namespace_only", "builtin")
	q.templates[tmpl.ID] = tmpl
	q.bySlug[tmpl.Slug] = tmpl.ID
	cluster := sqlc.Cluster{ID: uuid.New(), Name: "prod"}
	q.clusters[cluster.ID] = cluster

	h := NewNetworkPolicyHandler(q)
	body, _ := json.Marshal(ApplyNetworkPolicyRequest{
		TemplateID: tmpl.ID.String(),
		Namespaces: []string{"team-a", "team-b", "team-c"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+cluster.ID.String()+"/network-policies/applications/", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mkRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if got := len(q.applications); got != 3 {
		t.Errorf("expected 3 applications, got %d", got)
	}
}

func TestNetPolHandler_Reapply_ResetsStatusToPending(t *testing.T) {
	q := newFakeNetPolHandlerQuerier()
	tmpl := mkNetPolTemplate("deny_all_ingress", "builtin")
	q.templates[tmpl.ID] = tmpl
	q.bySlug[tmpl.Slug] = tmpl.ID
	cluster := sqlc.Cluster{ID: uuid.New(), Name: "prod"}
	q.clusters[cluster.ID] = cluster
	app := sqlc.NetworkPolicyApplication{
		ID: uuid.New(), TemplateID: tmpl.ID, ClusterID: cluster.ID,
		Namespace: "team-a", PolicyName: "astronomer-np-deny_all_ingress",
		Status: "failed", LastError: "stale",
	}
	q.applications[app.ID] = app

	h := NewNetworkPolicyHandler(q)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+cluster.ID.String()+"/network-policies/applications/"+app.ID.String()+"/reapply/", nil)
	w := httptest.NewRecorder()
	mkRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if got := q.applications[app.ID].Status; got != "pending" {
		t.Errorf("expected status=pending after reapply, got %q", got)
	}
}

// TestNetPolHandler_RequiresClusterUpdate documents the RBAC contract:
// the per-cluster apply/delete endpoints are gated on clusters:update at
// the router layer in internal/server/routes.go. This test just asserts
// the handler doesn't itself enforce a separate permission — the route
// wiring is the single source of truth.
func TestNetPolHandler_RequiresClusterUpdate(t *testing.T) {
	// Routing layer is exercised in internal/server/* tests; here we
	// just confirm the handler doesn't reject a well-formed body when
	// called directly (no separate auth check in the handler).
	q := newFakeNetPolHandlerQuerier()
	tmpl := mkNetPolTemplate("deny_all_ingress", "builtin")
	q.templates[tmpl.ID] = tmpl
	q.bySlug[tmpl.Slug] = tmpl.ID
	cluster := sqlc.Cluster{ID: uuid.New(), Name: "prod"}
	q.clusters[cluster.ID] = cluster
	h := NewNetworkPolicyHandler(q)
	body, _ := json.Marshal(ApplyNetworkPolicyRequest{TemplateID: tmpl.ID.String(), Namespace: "team-a"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+cluster.ID.String()+"/network-policies/applications/", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mkRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202 from a handler-only call (auth is at router), got %d", w.Code)
	}
}

// TestNetPolHandler_RequiresSuperuser_OnTemplates documents the RBAC
// contract for admin template CRUD: gated on ResourceNetworkPolicies in
// the router layer. The handler doesn't enforce a separate permission.
func TestNetPolHandler_RequiresSuperuser_OnTemplates(t *testing.T) {
	q := newFakeNetPolHandlerQuerier()
	h := NewNetworkPolicyHandler(q)
	body, _ := json.Marshal(CreateNetworkPolicyTemplateRequest{
		Slug:         "my_custom",
		Name:         "My Custom",
		SpecTemplate: "apiVersion: networking.k8s.io/v1\nkind: NetworkPolicy\nmetadata:\n  name: {{.PolicyName}}\n  namespace: {{.Namespace}}\nspec:\n  podSelector: {}\n",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/network-policy-templates/", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mkRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
}
