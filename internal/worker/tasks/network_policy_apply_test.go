package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// fakeNetPolQuerier records DB calls for the reconciler tests.
type fakeNetPolQuerier struct {
	mu sync.Mutex

	rows     map[uuid.UUID]sqlc.NetworkPolicyApplication
	tmpls    map[uuid.UUID]sqlc.NetworkPolicyTemplate
	statuses []string
}

func newFakeNetPolQuerier() *fakeNetPolQuerier {
	return &fakeNetPolQuerier{
		rows:  map[uuid.UUID]sqlc.NetworkPolicyApplication{},
		tmpls: map[uuid.UUID]sqlc.NetworkPolicyTemplate{},
	}
}

func (f *fakeNetPolQuerier) addTemplate(t sqlc.NetworkPolicyTemplate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tmpls[t.ID] = t
}

func (f *fakeNetPolQuerier) addRow(r sqlc.NetworkPolicyApplication) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[r.ID] = r
}

func (f *fakeNetPolQuerier) GetNetworkPolicyApplicationByID(_ context.Context, id uuid.UUID) (sqlc.NetworkPolicyApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[id]
	if !ok {
		return sqlc.NetworkPolicyApplication{}, pgx.ErrNoRows
	}
	return r, nil
}

func (f *fakeNetPolQuerier) ListPendingNetworkPolicyApplications(_ context.Context, _ int32) ([]sqlc.NetworkPolicyApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []sqlc.NetworkPolicyApplication{}
	for _, r := range f.rows {
		switch r.Status {
		case "pending", "failed", "drifting":
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeNetPolQuerier) ListAppliedNetworkPolicyApplications(_ context.Context, _ int32) ([]sqlc.NetworkPolicyApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []sqlc.NetworkPolicyApplication{}
	for _, r := range f.rows {
		if r.Status == "applied" {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeNetPolQuerier) MarkNetworkPolicyApplicationStatus(_ context.Context, arg sqlc.MarkNetworkPolicyApplicationStatusParams) (sqlc.NetworkPolicyApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[arg.ID]
	if !ok {
		return sqlc.NetworkPolicyApplication{}, pgx.ErrNoRows
	}
	r.Status = arg.Status
	r.LastError = arg.LastError
	f.rows[arg.ID] = r
	f.statuses = append(f.statuses, arg.Status)
	return r, nil
}

func (f *fakeNetPolQuerier) GetNetworkPolicyTemplateByID(_ context.Context, id uuid.UUID) (sqlc.NetworkPolicyTemplate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tmpls[id]
	if !ok {
		return sqlc.NetworkPolicyTemplate{}, pgx.ErrNoRows
	}
	return t, nil
}

// fakeK8sRequester captures the most recent K8s request so assertions
// can inspect the SSA path / method / body.
type fakeK8sRequester struct {
	mu       sync.Mutex
	requests []k8sCall

	respStatus int
	respBody   string
	respErr    error
}

type k8sCall struct {
	clusterID string
	method    string
	path      string
	body      []byte
}

func (f *fakeK8sRequester) Do(_ context.Context, clusterID, method, path string, body []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, k8sCall{clusterID: clusterID, method: method, path: path, body: body})
	if f.respErr != nil {
		return nil, f.respErr
	}
	status := f.respStatus
	if status == 0 {
		status = 200
	}
	return &protocol.K8sResponsePayload{StatusCode: status, Body: f.respBody}, nil
}

func mkTemplate(slug string) sqlc.NetworkPolicyTemplate {
	return sqlc.NetworkPolicyTemplate{
		ID:           uuid.New(),
		Slug:         slug,
		Name:         slug,
		Kind:         "builtin",
		SpecTemplate: "apiVersion: networking.k8s.io/v1\nkind: NetworkPolicy\nmetadata:\n  name: {{.PolicyName}}\n  namespace: {{.Namespace}}\nspec:\n  podSelector: {}\n  policyTypes: [Ingress]\n",
		Enabled:      true,
	}
}

func mkApplication(tmplID uuid.UUID, slug, ns string) sqlc.NetworkPolicyApplication {
	return sqlc.NetworkPolicyApplication{
		ID:         uuid.New(),
		TemplateID: tmplID,
		ClusterID:  uuid.New(),
		Namespace:  ns,
		PolicyName: "astronomer-np-" + slug,
		Status:     "pending",
	}
}

func TestReconciler_SSAsNewNetworkPolicy(t *testing.T) {
	defer ResetNetworkPolicyApply()
	q := newFakeNetPolQuerier()
	tmpl := mkTemplate("deny_all_ingress")
	q.addTemplate(tmpl)
	row := mkApplication(tmpl.ID, tmpl.Slug, "team-a")
	q.addRow(row)

	k8s := &fakeK8sRequester{respStatus: 200}
	deps := NetworkPolicyApplyDeps{Queries: q, Requester: k8s}

	if err := runNetworkPolicyApplySweep(context.Background(), deps); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got := len(k8s.requests); got != 1 {
		t.Fatalf("expected 1 K8s request, got %d", got)
	}
	req := k8s.requests[0]
	if req.method != "PATCH" {
		t.Errorf("expected PATCH (SSA), got %q", req.method)
	}
	if want := "/apis/networking.k8s.io/v1/namespaces/team-a/networkpolicies/astronomer-np-deny_all_ingress"; req.path[:len(want)] != want {
		t.Errorf("expected path prefix %q, got %q", want, req.path)
	}
}

func TestReconciler_MarksAppliedOnSuccess(t *testing.T) {
	defer ResetNetworkPolicyApply()
	q := newFakeNetPolQuerier()
	tmpl := mkTemplate("namespace_only")
	q.addTemplate(tmpl)
	row := mkApplication(tmpl.ID, tmpl.Slug, "team-a")
	q.addRow(row)
	k8s := &fakeK8sRequester{respStatus: 200}
	deps := NetworkPolicyApplyDeps{Queries: q, Requester: k8s}

	if err := runNetworkPolicyApplyOne(context.Background(), deps, row.ID); err != nil {
		t.Fatalf("apply one: %v", err)
	}
	got := q.rows[row.ID]
	if got.Status != "applied" {
		t.Errorf("expected status=applied, got %q (err=%q)", got.Status, got.LastError)
	}
}

func TestReconciler_MarksFailedOnK8sError(t *testing.T) {
	defer ResetNetworkPolicyApply()
	q := newFakeNetPolQuerier()
	tmpl := mkTemplate("project_isolated")
	q.addTemplate(tmpl)
	row := mkApplication(tmpl.ID, tmpl.Slug, "team-a")
	q.addRow(row)
	k8s := &fakeK8sRequester{respErr: errors.New("tunnel down")}
	deps := NetworkPolicyApplyDeps{Queries: q, Requester: k8s}

	if err := runNetworkPolicyApplyOne(context.Background(), deps, row.ID); err != nil {
		t.Fatalf("apply one: %v", err)
	}
	got := q.rows[row.ID]
	if got.Status != "failed" {
		t.Errorf("expected status=failed, got %q", got.Status)
	}
	if got.LastError == "" {
		t.Errorf("expected last_error to be set")
	}
}

func TestDriftCheck_DetectsDivergence(t *testing.T) {
	defer ResetNetworkPolicyApply()
	q := newFakeNetPolQuerier()
	tmpl := mkTemplate("deny_all_ingress")
	q.addTemplate(tmpl)
	row := mkApplication(tmpl.ID, tmpl.Slug, "team-a")
	row.Status = "applied"
	q.addRow(row)

	// Live NetworkPolicy is missing the managed-by label — drift.
	body, _ := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"labels": map[string]string{},
		},
	})
	k8s := &fakeK8sRequester{respStatus: 200, respBody: string(body)}
	deps := NetworkPolicyApplyDeps{Queries: q, Requester: k8s}

	if err := runNetworkPolicyDriftSweep(context.Background(), deps); err != nil {
		t.Fatalf("drift sweep: %v", err)
	}
	got := q.rows[row.ID]
	if got.Status != "drifting" {
		t.Errorf("expected status=drifting, got %q", got.Status)
	}
}

func TestDriftCheck_NoDriftOnLabelMatch(t *testing.T) {
	defer ResetNetworkPolicyApply()
	q := newFakeNetPolQuerier()
	tmpl := mkTemplate("deny_all_ingress")
	q.addTemplate(tmpl)
	row := mkApplication(tmpl.ID, tmpl.Slug, "team-a")
	row.Status = "applied"
	q.addRow(row)

	body, _ := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "astronomer",
				"astronomer.io/template":       tmpl.Slug,
			},
		},
	})
	k8s := &fakeK8sRequester{respStatus: 200, respBody: string(body)}
	deps := NetworkPolicyApplyDeps{Queries: q, Requester: k8s}

	if err := runNetworkPolicyDriftSweep(context.Background(), deps); err != nil {
		t.Fatalf("drift sweep: %v", err)
	}
	got := q.rows[row.ID]
	if got.Status != "applied" {
		t.Errorf("expected status=applied (no drift), got %q", got.Status)
	}
}

func TestDeleteNetworkPolicyInCluster_Smoke(t *testing.T) {
	k8s := &fakeK8sRequester{respStatus: 200}
	if err := DeleteNetworkPolicyInCluster(context.Background(), k8s, uuid.New(), "team-a", "astronomer-np-deny_all_ingress"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := len(k8s.requests); got != 1 || k8s.requests[0].method != "DELETE" {
		t.Errorf("expected 1 DELETE, got %+v", k8s.requests)
	}
}
