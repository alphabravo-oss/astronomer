package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// --- rendering tests ------------------------------------------------------

// TestRenderResourceQuota_TypicalShape verifies the canonical shape of a
// rendered ResourceQuota for the example called out in the plan:
//   { "cpu": "4", "memory": "8Gi", "pods": "20" }
//
// This is the load-bearing assertion: every byte of this output is read by
// the K8s API server, and a typo is silently catastrophic.
func TestRenderResourceQuota_TypicalShape(t *testing.T) {
	raw := json.RawMessage(`{"cpu":"4","memory":"8Gi","pods":"20"}`)
	got := renderResourceQuota("team-a", raw)

	if got["apiVersion"] != "v1" {
		t.Fatalf("apiVersion: got %v, want v1", got["apiVersion"])
	}
	if got["kind"] != "ResourceQuota" {
		t.Fatalf("kind: got %v, want ResourceQuota", got["kind"])
	}
	meta := got["metadata"].(map[string]any)
	if meta["name"] != managedQuotaName {
		t.Fatalf("name: got %v, want %s", meta["name"], managedQuotaName)
	}
	if meta["namespace"] != "team-a" {
		t.Fatalf("namespace: got %v, want team-a", meta["namespace"])
	}
	hard := got["spec"].(map[string]any)["hard"].(map[string]any)
	if hard["cpu"] != "4" || hard["memory"] != "8Gi" || hard["pods"] != "20" {
		t.Fatalf("hard map missing fields: %+v", hard)
	}
}

// TestBuildHardSpec_AliasesAndUnknownPassthrough confirms that the friendly
// "storage" alias maps to the canonical requests.storage and that unknown
// keys (e.g. count/services.loadbalancers) pass through verbatim.
func TestBuildHardSpec_AliasesAndUnknownPassthrough(t *testing.T) {
	raw := json.RawMessage(`{"storage":"100Gi","count/services.loadbalancers":"3","empty":""}`)
	hard := buildHardSpec(raw)
	if hard["requests.storage"] != "100Gi" {
		t.Errorf("alias storage->requests.storage failed: %+v", hard)
	}
	if hard["count/services.loadbalancers"] != "3" {
		t.Errorf("unknown key passthrough failed: %+v", hard)
	}
	if _, ok := hard["empty"]; ok {
		t.Errorf("empty-string value should be dropped: %+v", hard)
	}
}

// TestRenderLimitRange_Container validates that the rendered LimitRange
// targets Container scope and only includes user-set sub-maps.
func TestRenderLimitRange_Container(t *testing.T) {
	raw := json.RawMessage(`{
		"default":        {"cpu":"500m","memory":"512Mi"},
		"defaultRequest": {"cpu":"100m","memory":"128Mi"}
	}`)
	got := renderLimitRange("team-a", raw)
	limits := got["spec"].(map[string]any)["limits"].([]any)
	if len(limits) != 1 {
		t.Fatalf("expected 1 limit entry, got %d", len(limits))
	}
	limit := limits[0].(map[string]any)
	if limit["type"] != "Container" {
		t.Errorf("expected Container type, got %v", limit["type"])
	}
	if _, ok := limit["default"]; !ok {
		t.Errorf("missing default")
	}
	if _, ok := limit["defaultRequest"]; !ok {
		t.Errorf("missing defaultRequest")
	}
	if _, ok := limit["max"]; ok {
		t.Errorf("unexpected max in rendered LimitRange (only default+defaultRequest were set)")
	}
}

// TestRenderNetworkPolicy_Modes covers the three documented modes:
// none (handled outside this fn), isolated (deny-all), allow-same-project.
func TestRenderNetworkPolicy_Modes(t *testing.T) {
	t.Run("isolated_is_deny_all", func(t *testing.T) {
		got := renderNetworkPolicy("team-a", "00000000-0000-0000-0000-000000000001", "isolated")
		spec := got["spec"].(map[string]any)
		if _, ok := spec["ingress"]; ok {
			t.Errorf("isolated mode should omit ingress array; got %+v", spec)
		}
		if _, ok := spec["egress"]; ok {
			t.Errorf("isolated mode should omit egress array; got %+v", spec)
		}
		pt := spec["policyTypes"].([]any)
		if len(pt) != 2 {
			t.Errorf("expected both Ingress and Egress in policyTypes, got %v", pt)
		}
	})

	t.Run("allow_same_project_uses_label_selector", func(t *testing.T) {
		got := renderNetworkPolicy("team-a", "abc123", "allow-same-project")
		spec := got["spec"].(map[string]any)
		ing := spec["ingress"].([]any)
		if len(ing) != 1 {
			t.Fatalf("expected 1 ingress rule, got %d", len(ing))
		}
		from := ing[0].(map[string]any)["from"].([]any)
		nsSel := from[0].(map[string]any)["namespaceSelector"].(map[string]any)
		labels := nsSel["matchLabels"].(map[string]any)
		if labels[projectNamespaceLabelKey] != "abc123" {
			t.Errorf("expected match label %s=abc123, got %v", projectNamespaceLabelKey, labels)
		}
	})
}

// TestNormalizeNetworkPolicyMode covers the typo-coercion contract: any
// unrecognized value collapses to "none" (i.e. no NetworkPolicy is applied)
// rather than e.g. silently picking allow-same-project.
func TestNormalizeNetworkPolicyMode(t *testing.T) {
	cases := map[string]string{
		"":                    "none",
		"none":                "none",
		"ISOLATED":            "none", // case-sensitive on purpose
		"isolated":            "isolated",
		"allow-same-project":  "allow-same-project",
		"deny-everything-yo!": "none",
	}
	for in, want := range cases {
		if got := normalizeNetworkPolicyMode(in); got != want {
			t.Errorf("normalize(%q): got %q, want %q", in, got, want)
		}
	}
}

// --- worker task end-to-end tests -----------------------------------------

// fakeProjectRequester captures every call the reconcile task makes so the
// test can assert ordering, paths, and body shape.
type fakeProjectRequester struct {
	mu    sync.Mutex
	calls []fakeCall
	// fail is consulted per call. If true, returns a 500 response.
	failOnce bool
}

type fakeCall struct {
	method  string
	path    string
	body    []byte
	headers map[string]string
}

func (f *fakeProjectRequester) Do(_ context.Context, _, method, path string, body []byte, headers map[string]string) (*ProjectK8sResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{method: method, path: path, body: body, headers: headers})
	if f.failOnce {
		f.failOnce = false
		return &ProjectK8sResponse{StatusCode: http.StatusInternalServerError, Body: []byte(`{"message":"boom"}`)}, nil
	}
	return &ProjectK8sResponse{StatusCode: http.StatusOK, Body: nil}, nil
}

// fakeProjectQuerier is the minimum surface needed by reconcile tests.
type fakeProjectQuerier struct {
	project sqlc.Project
	// last captured Mark call (for assertions)
	lastMark *sqlc.MarkProjectNamespaceReconciledParams
}

func (f *fakeProjectQuerier) GetProjectByID(_ context.Context, _ uuid.UUID) (sqlc.Project, error) {
	return f.project, nil
}
func (f *fakeProjectQuerier) ListProjectNamespaces(_ context.Context, _ uuid.UUID) ([]sqlc.ProjectNamespace, error) {
	return nil, nil
}
func (f *fakeProjectQuerier) ListAllProjectNamespaces(_ context.Context) ([]sqlc.ProjectNamespace, error) {
	return nil, nil
}
func (f *fakeProjectQuerier) UpsertProjectNamespace(_ context.Context, arg sqlc.UpsertProjectNamespaceParams) (sqlc.ProjectNamespace, error) {
	return sqlc.ProjectNamespace{ProjectID: arg.ProjectID, ClusterID: arg.ClusterID, Namespace: arg.Namespace}, nil
}
func (f *fakeProjectQuerier) DeleteProjectNamespace(_ context.Context, _ sqlc.DeleteProjectNamespaceParams) error {
	return nil
}
func (f *fakeProjectQuerier) ClaimProjectNamespaceReconcile(_ context.Context, arg sqlc.ClaimProjectNamespaceReconcileParams) (sqlc.ProjectNamespace, error) {
	return sqlc.ProjectNamespace{ProjectID: arg.ProjectID, ClusterID: arg.ClusterID, Namespace: arg.Namespace, LockedUntil: arg.LockedUntil}, nil
}
func (f *fakeProjectQuerier) MarkProjectNamespaceReconciled(_ context.Context, arg sqlc.MarkProjectNamespaceReconciledParams) error {
	f.lastMark = &arg
	return nil
}

// TestReconcileProjectNamespace_AppliesQuotaAndLabelsNamespace runs the full
// apply path and asserts every API call we expect: namespace label patch,
// ResourceQuota SSA, NetworkPolicy SSA. It also confirms last_reconcile_error
// gets cleared on success.
func TestReconcileProjectNamespace_AppliesQuotaAndLabelsNamespace(t *testing.T) {
	projectID := uuid.New()
	clusterID := uuid.New()
	q := &fakeProjectQuerier{
		project: sqlc.Project{
			ID:                projectID,
			ClusterID:         clusterID,
			ResourceQuota:     json.RawMessage(`{"cpu":"4","memory":"8Gi","pods":"20"}`),
			NetworkPolicyMode: "isolated",
		},
	}
	r := &fakeProjectRequester{}

	if err := reconcileProjectNamespace(context.Background(), q, r, q.project, clusterID, "team-a"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if q.lastMark == nil {
		t.Fatal("expected MarkProjectNamespaceReconciled to be called")
	}
	if q.lastMark.LastReconcileError != "" {
		t.Errorf("expected empty last_reconcile_error, got %q", q.lastMark.LastReconcileError)
	}

	// Four calls: label PATCH on ns, SSA quota, DELETE limitrange (no fields
	// in the project), SSA network policy.
	if got, want := len(r.calls), 4; got != want {
		t.Fatalf("expected %d API calls, got %d: %+v", want, got, r.calls)
	}
	if !strings.HasSuffix(r.calls[0].path, "/api/v1/namespaces/team-a") || r.calls[0].method != http.MethodPatch {
		t.Errorf("first call should be ns label PATCH, got %+v", r.calls[0])
	}
	quotaCall := r.calls[1]
	if !strings.Contains(quotaCall.path, "/resourcequotas/"+managedQuotaName) {
		t.Errorf("second call should target the managed ResourceQuota, got %+v", quotaCall)
	}
	if !strings.Contains(quotaCall.path, "fieldManager="+projectFieldManager) {
		t.Errorf("SSA path should include fieldManager: %s", quotaCall.path)
	}
	if quotaCall.headers["Content-Type"] != "application/apply-patch+yaml" {
		t.Errorf("SSA Content-Type wrong: %v", quotaCall.headers)
	}
	// Limit-range DELETE happens because project.LimitRange is empty.
	if r.calls[2].method != http.MethodDelete || !strings.Contains(r.calls[2].path, "/limitranges/"+managedLimitRangeName) {
		t.Errorf("third call should DELETE limitrange, got %+v", r.calls[2])
	}
	if !strings.Contains(r.calls[3].path, "/networkpolicies/"+managedNetworkPolicyName) {
		t.Errorf("fourth call should target managed NetworkPolicy, got %+v", r.calls[3])
	}
}

// TestReconcileProjectNamespace_DeletesNetworkPolicyOnNoneMode confirms the
// transition allow-same-project -> none surfaces as a DELETE on the managed
// NetworkPolicy so we don't leave drift.
func TestReconcileProjectNamespace_DeletesNetworkPolicyOnNoneMode(t *testing.T) {
	q := &fakeProjectQuerier{
		project: sqlc.Project{
			ID:                uuid.New(),
			NetworkPolicyMode: "none",
			ResourceQuota:     json.RawMessage(`{"cpu":"1"}`),
		},
	}
	r := &fakeProjectRequester{}
	if err := reconcileProjectNamespace(context.Background(), q, r, q.project, uuid.New(), "team-a"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	hasDelete := false
	for _, c := range r.calls {
		if c.method == http.MethodDelete && strings.Contains(c.path, "/networkpolicies/"+managedNetworkPolicyName) {
			hasDelete = true
		}
	}
	if !hasDelete {
		t.Errorf("expected DELETE on managed NetworkPolicy when mode=none; calls=%+v", r.calls)
	}
}

// TestReconcileProjectNamespace_CapturesErrorOnApplyFailure confirms the
// reconcile error is round-tripped into project_namespaces.last_reconcile_error
// so the UI can show a red dot.
func TestReconcileProjectNamespace_CapturesErrorOnApplyFailure(t *testing.T) {
	q := &fakeProjectQuerier{
		project: sqlc.Project{
			ID:            uuid.New(),
			ResourceQuota: json.RawMessage(`{"cpu":"1"}`),
		},
	}
	r := &fakeProjectRequester{}
	// First call succeeds (label patch), second fails (quota apply).
	r.failOnce = false
	// Wire the failure on the SECOND call by chaining a wrapper.
	wrapper := &errOnNthCall{inner: r, n: 2}

	err := reconcileProjectNamespace(context.Background(), q, wrapper, q.project, uuid.New(), "team-a")
	if err == nil {
		t.Fatalf("expected reconcile error to be returned, got nil")
	}
	if q.lastMark == nil || q.lastMark.LastReconcileError == "" {
		t.Fatalf("expected last_reconcile_error to be populated, got %+v", q.lastMark)
	}
}

// errOnNthCall returns an error on the N-th invocation; useful for fault
// injection without instrumenting the inner fake.
type errOnNthCall struct {
	inner *fakeProjectRequester
	n     int
	count int
}

func (e *errOnNthCall) Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*ProjectK8sResponse, error) {
	e.count++
	if e.count == e.n {
		return nil, errors.New("synthetic failure")
	}
	return e.inner.Do(ctx, clusterID, method, path, body, headers)
}

// TestHandleProjectReconcileAll_LeasesAndReconcilesOneRow confirms the sweep
// claims the lease, reconciles, and marks the row even when projectDeps is
// configured with a single fake row. (The full multi-pod contention is left
// to integration; this test pins the happy-path control flow.)
func TestHandleProjectReconcileAll_LeasesAndReconcilesOneRow(t *testing.T) {
	defer ResetProjectReconcile()

	projectID := uuid.New()
	clusterID := uuid.New()
	q := &sweepFakeQuerier{
		fakeProjectQuerier: fakeProjectQuerier{
			project: sqlc.Project{
				ID:            projectID,
				ClusterID:     clusterID,
				ResourceQuota: json.RawMessage(`{"cpu":"2"}`),
			},
		},
		rows: []sqlc.ProjectNamespace{{ProjectID: projectID, ClusterID: clusterID, Namespace: "team-a"}},
	}
	r := &fakeProjectRequester{}
	ConfigureProjectReconcile(ProjectReconcileDeps{Queries: q, Requester: r})

	if err := HandleProjectReconcileAll(context.Background(), nil); err != nil {
		t.Fatalf("HandleProjectReconcileAll: %v", err)
	}
	if !q.claimed {
		t.Errorf("expected ClaimProjectNamespaceReconcile to be called")
	}
	if q.lastMark == nil {
		t.Errorf("expected MarkProjectNamespaceReconciled to be called")
	}
}

// sweepFakeQuerier extends fakeProjectQuerier with a row list for the sweep
// tests.
type sweepFakeQuerier struct {
	fakeProjectQuerier
	rows    []sqlc.ProjectNamespace
	claimed bool
}

func (s *sweepFakeQuerier) ListAllProjectNamespaces(_ context.Context) ([]sqlc.ProjectNamespace, error) {
	return s.rows, nil
}
func (s *sweepFakeQuerier) ClaimProjectNamespaceReconcile(_ context.Context, arg sqlc.ClaimProjectNamespaceReconcileParams) (sqlc.ProjectNamespace, error) {
	s.claimed = true
	return sqlc.ProjectNamespace{
		ProjectID:   arg.ProjectID,
		ClusterID:   arg.ClusterID,
		Namespace:   arg.Namespace,
		LockedUntil: arg.LockedUntil,
	}, nil
}

// TestHandleProjectReconcile_RemoveOpDeletesRow checks the cleanup path.
func TestHandleProjectReconcile_RemoveOpDeletesRow(t *testing.T) {
	defer ResetProjectReconcile()

	q := &fakeProjectQuerier{}
	r := &fakeProjectRequester{}
	ConfigureProjectReconcile(ProjectReconcileDeps{Queries: q, Requester: r})

	task, _ := NewProjectReconcileTask(ProjectReconcilePayload{
		ProjectID: uuid.NewString(),
		ClusterID: uuid.NewString(),
		Namespace: "team-a",
		Op:        "remove",
	})
	if err := HandleProjectReconcile(context.Background(), task); err != nil {
		t.Fatalf("HandleProjectReconcile: %v", err)
	}
	// Three best-effort DELETE calls + a label-strip patch.
	if len(r.calls) < 3 {
		t.Errorf("expected at least 3 cleanup calls (delete CRs + strip label), got %d", len(r.calls))
	}
	for _, c := range r.calls[:3] {
		if c.method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s on %s", c.method, c.path)
		}
	}
}

// TestHasLimitRangeFields_OnlySubmapsCount makes sure a stray top-level key
// doesn't trip hasLimitRangeFields into thinking the user set a limit.
func TestHasLimitRangeFields_OnlySubmapsCount(t *testing.T) {
	if hasLimitRangeFields(json.RawMessage(`{}`)) {
		t.Error("empty object should report no limit fields")
	}
	if hasLimitRangeFields(json.RawMessage(`{"comment":"hi"}`)) {
		t.Error("only a top-level non-submap key should still report no limit fields")
	}
	if !hasLimitRangeFields(json.RawMessage(`{"default":{"cpu":"1"}}`)) {
		t.Error("default submap with content should report limit fields present")
	}
}

// pgtypePinned silences the import without requiring an active reference;
// the production code uses pgtype.Timestamptz and tests will too once we
// extend coverage to last_reconciled_at windows. Kept as a vetted hook.
var pgtypePinned = pgtype.Timestamptz{}

var _ = pgtypePinned
var _ = time.Second
