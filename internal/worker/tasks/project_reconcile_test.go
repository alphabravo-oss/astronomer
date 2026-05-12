package tasks

import (
	"context"
	"encoding/base64"
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
//
//	{ "cpu": "4", "memory": "8Gi", "pods": "20" }
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

func TestRenderLimitRange_AcceptsSnakeCaseDefaultRequest(t *testing.T) {
	raw := json.RawMessage(`{
		"default": {"cpu":"500m","memory":"512Mi"},
		"default_request": {"cpu":"100m","memory":"128Mi"}
	}`)
	got := renderLimitRange("team-a", raw)
	limit := got["spec"].(map[string]any)["limits"].([]any)[0].(map[string]any)
	defaultRequest, ok := limit["defaultRequest"].(map[string]any)
	if !ok {
		t.Fatalf("missing defaultRequest in rendered LimitRange: %+v", limit)
	}
	if defaultRequest["cpu"] != "100m" || defaultRequest["memory"] != "128Mi" {
		t.Fatalf("unexpected defaultRequest: %+v", defaultRequest)
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

func TestHasLimitRangeFields_AcceptsSnakeCaseDefaultRequest(t *testing.T) {
	if !hasLimitRangeFields(json.RawMessage(`{"default_request":{"cpu":"100m"}}`)) {
		t.Fatal("expected snake_case default_request to count as a limit-range field")
	}
}

// --- worker task end-to-end tests -----------------------------------------

// fakeProjectRequester captures every call the reconcile task makes so the
// test can assert ordering, paths, and body shape.
type fakeProjectRequester struct {
	mu    sync.Mutex
	calls []fakeCall
	// fail is consulted per call. If true, returns a 500 response.
	failOnce                         bool
	defaultServiceAccountPullSecrets []string
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
	if method == http.MethodGet && strings.Contains(path, "/serviceaccounts/default") {
		type saPullSecret struct {
			Name string `json:"name"`
		}
		resp := struct {
			ImagePullSecrets []saPullSecret `json:"imagePullSecrets"`
		}{}
		for _, item := range f.defaultServiceAccountPullSecrets {
			resp.ImagePullSecrets = append(resp.ImagePullSecrets, saPullSecret{Name: item})
		}
		raw, _ := json.Marshal(resp)
		return &ProjectK8sResponse{StatusCode: http.StatusOK, Body: raw}, nil
	}
	if f.failOnce {
		f.failOnce = false
		return &ProjectK8sResponse{StatusCode: http.StatusInternalServerError, Body: []byte(`{"message":"boom"}`)}, nil
	}
	if method == http.MethodPatch && strings.Contains(path, "/serviceaccounts/default") {
		var patch struct {
			ImagePullSecrets []struct {
				Name string `json:"name"`
			} `json:"imagePullSecrets"`
		}
		if err := json.Unmarshal(body, &patch); err == nil {
			f.defaultServiceAccountPullSecrets = f.defaultServiceAccountPullSecrets[:0]
			for _, item := range patch.ImagePullSecrets {
				f.defaultServiceAccountPullSecrets = append(f.defaultServiceAccountPullSecrets, item.Name)
			}
		}
	}
	return &ProjectK8sResponse{StatusCode: http.StatusOK, Body: nil}, nil
}

// fakeProjectQuerier is the minimum surface needed by reconcile tests.
type fakeProjectQuerier struct {
	project            sqlc.Project
	registryConfig     sqlc.ClusterRegistryConfig
	registryConfigErr  error
	defaultTemplate    sqlc.PodSecurityTemplate
	defaultTemplateErr error
	// last captured Mark call (for assertions)
	lastMark *sqlc.MarkProjectNamespaceReconciledParams
}

func (f *fakeProjectQuerier) GetProjectByID(_ context.Context, _ uuid.UUID) (sqlc.Project, error) {
	return f.project, nil
}
func (f *fakeProjectQuerier) GetClusterRegistryConfig(_ context.Context, _ uuid.UUID) (sqlc.ClusterRegistryConfig, error) {
	if f.registryConfigErr != nil {
		return sqlc.ClusterRegistryConfig{}, f.registryConfigErr
	}
	return f.registryConfig, nil
}
func (f *fakeProjectQuerier) GetDefaultPodSecurityTemplate(_ context.Context) (sqlc.PodSecurityTemplate, error) {
	if f.defaultTemplateErr != nil {
		return sqlc.PodSecurityTemplate{}, f.defaultTemplateErr
	}
	return f.defaultTemplate, nil
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
		registryConfigErr: errors.New("no rows in result set"),
		defaultTemplate: sqlc.PodSecurityTemplate{
			EnforceLevel:   "baseline",
			EnforceVersion: "latest",
			AuditLevel:     "restricted",
			AuditVersion:   "latest",
			WarnLevel:      "restricted",
			WarnVersion:    "latest",
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

	// Seven calls: label PATCH on ns, SSA legacy quota, DELETE
	// astronomer-project-quota (empty per-project policy in this fixture),
	// DELETE limitrange (no fields in the project), SSA network policy,
	// GET default SA, DELETE managed registry secret. No registry config means
	// reconcile only does a cleanup GET on the default SA plus the DELETE.
	if got, want := len(r.calls), 7; got != want {
		t.Fatalf("expected %d API calls, got %d: %+v", want, got, r.calls)
	}
	if !strings.HasSuffix(r.calls[0].path, "/api/v1/namespaces/team-a") || r.calls[0].method != http.MethodPatch {
		t.Errorf("first call should be ns label PATCH, got %+v", r.calls[0])
	}
	var namespacePatch map[string]any
	if err := json.Unmarshal(r.calls[0].body, &namespacePatch); err != nil {
		t.Fatalf("unmarshal namespace patch: %v", err)
	}
	labels := namespacePatch["metadata"].(map[string]any)["labels"].(map[string]any)
	if labels[projectNamespaceLabelKey] != projectID.String() {
		t.Fatalf("expected namespace patch to set project label, got %+v", labels)
	}
	if labels["pod-security.kubernetes.io/enforce"] != "baseline" {
		t.Fatalf("expected namespace patch to set PSA labels, got %+v", labels)
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
	// astronomer-project-quota DELETE happens because the per-project policy
	// columns are all empty in this fixture.
	if r.calls[2].method != http.MethodDelete || !strings.Contains(r.calls[2].path, "/resourcequotas/"+managedProjectQuotaName) {
		t.Errorf("third call should DELETE the project quota, got %+v", r.calls[2])
	}
	// Limit-range DELETE happens because project.LimitRange is empty.
	if r.calls[3].method != http.MethodDelete || !strings.Contains(r.calls[3].path, "/limitranges/"+managedLimitRangeName) {
		t.Errorf("fourth call should DELETE limitrange, got %+v", r.calls[3])
	}
	if !strings.Contains(r.calls[4].path, "/networkpolicies/"+managedNetworkPolicyName) {
		t.Errorf("fifth call should target managed NetworkPolicy, got %+v", r.calls[4])
	}
	if r.calls[5].method != http.MethodGet || !strings.Contains(r.calls[5].path, "/serviceaccounts/default") {
		t.Errorf("sixth call should GET the default serviceaccount, got %+v", r.calls[5])
	}
	if r.calls[6].method != http.MethodDelete || !strings.Contains(r.calls[6].path, "/secrets/"+managedRegistrySecretName) {
		t.Errorf("seventh call should cleanup managed registry secret, got %+v", r.calls[6])
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
		registryConfigErr: errors.New("no rows in result set"),
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

func TestProjectNamespaceLabels_UsesDefaultPodSecurityTemplate(t *testing.T) {
	projectID := uuid.NewString()
	q := &fakeProjectQuerier{
		defaultTemplate: sqlc.PodSecurityTemplate{
			EnforceLevel:   "baseline",
			EnforceVersion: "latest",
			AuditLevel:     "restricted",
			AuditVersion:   "latest",
			WarnLevel:      "restricted",
			WarnVersion:    "latest",
		},
	}
	labels, err := projectNamespaceLabels(context.Background(), q, "team-a", projectID)
	if err != nil {
		t.Fatalf("projectNamespaceLabels: %v", err)
	}
	if labels[projectNamespaceLabelKey] != projectID {
		t.Fatalf("expected project label to be preserved, got %+v", labels)
	}
	if labels["pod-security.kubernetes.io/enforce"] != "baseline" {
		t.Fatalf("expected enforce label, got %+v", labels)
	}
	if labels["pod-security.kubernetes.io/audit"] != "restricted" {
		t.Fatalf("expected audit label, got %+v", labels)
	}
	if labels["pod-security.kubernetes.io/warn-version"] != "latest" {
		t.Fatalf("expected warn version label, got %+v", labels)
	}
}

func TestProjectNamespaceLabels_SkipsExemptNamespace(t *testing.T) {
	projectID := uuid.NewString()
	q := &fakeProjectQuerier{
		defaultTemplate: sqlc.PodSecurityTemplate{
			EnforceLevel:     "baseline",
			ExemptNamespaces: json.RawMessage(`["kube-system","team-a"]`),
		},
	}
	labels, err := projectNamespaceLabels(context.Background(), q, "team-a", projectID)
	if err != nil {
		t.Fatalf("projectNamespaceLabels: %v", err)
	}
	if labels[projectNamespaceLabelKey] != projectID {
		t.Fatalf("expected project label to be preserved, got %+v", labels)
	}
	if _, ok := labels["pod-security.kubernetes.io/enforce"]; ok {
		t.Fatalf("expected exempt namespace to skip PSA labels, got %+v", labels)
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
		registryConfigErr: errors.New("no rows in result set"),
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
			registryConfigErr: errors.New("no rows in result set"),
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

func TestReconcileProjectNamespace_PropagatesRegistrySecretAndServiceAccount(t *testing.T) {
	projectID := uuid.New()
	clusterID := uuid.New()
	q := &fakeProjectQuerier{
		project: sqlc.Project{
			ID:                projectID,
			ClusterID:         clusterID,
			ResourceQuota:     json.RawMessage(`{"cpu":"1"}`),
			NetworkPolicyMode: "none",
		},
		registryConfig: sqlc.ClusterRegistryConfig{
			ClusterID:          clusterID,
			PrivateRegistryUrl: "https://registry.example.com/team",
			RegistryUsername:   "alice",
			RegistryPassword:   "secret",
		},
	}
	r := &fakeProjectRequester{
		defaultServiceAccountPullSecrets: []string{"existing-secret"},
	}

	if err := reconcileProjectNamespace(context.Background(), q, r, q.project, clusterID, "team-a"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var secretCall *fakeCall
	var saPatch *fakeCall
	for i := range r.calls {
		call := &r.calls[i]
		if call.method == http.MethodPatch && strings.Contains(call.path, "/secrets/"+managedRegistrySecretName) {
			secretCall = call
		}
		if call.method == http.MethodPatch && strings.Contains(call.path, "/serviceaccounts/default") {
			saPatch = call
		}
	}
	if secretCall == nil {
		t.Fatalf("expected managed registry secret apply call, got %+v", r.calls)
	}
	if saPatch == nil {
		t.Fatalf("expected default serviceaccount patch, got %+v", r.calls)
	}
	var secretDoc struct {
		Type string            `json:"type"`
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(secretCall.body, &secretDoc); err != nil {
		t.Fatalf("unmarshal secret body: %v", err)
	}
	if secretDoc.Type != "kubernetes.io/dockerconfigjson" {
		t.Fatalf("unexpected secret type %q", secretDoc.Type)
	}
	rawDockerCfg, err := base64.StdEncoding.DecodeString(secretDoc.Data[".dockerconfigjson"])
	if err != nil {
		t.Fatalf("decode docker config: %v", err)
	}
	var dockerCfg map[string]any
	if err := json.Unmarshal(rawDockerCfg, &dockerCfg); err != nil {
		t.Fatalf("unmarshal docker config: %v", err)
	}
	auths := dockerCfg["auths"].(map[string]any)
	entry := auths["registry.example.com/team"].(map[string]any)
	if entry["username"] != "alice" || entry["password"] != "secret" {
		t.Fatalf("unexpected docker auth entry: %+v", entry)
	}
	var saDoc struct {
		ImagePullSecrets []struct {
			Name string `json:"name"`
		} `json:"imagePullSecrets"`
	}
	if err := json.Unmarshal(saPatch.body, &saDoc); err != nil {
		t.Fatalf("unmarshal serviceaccount patch: %v", err)
	}
	if got := []string{saDoc.ImagePullSecrets[0].Name, saDoc.ImagePullSecrets[1].Name}; !strings.Contains(strings.Join(got, ","), "existing-secret") || !strings.Contains(strings.Join(got, ","), managedRegistrySecretName) {
		t.Fatalf("expected existing and managed pull secrets, got %+v", saDoc.ImagePullSecrets)
	}
}

func TestRemoveProjectRegistryAccess_StripsManagedServiceAccountSecret(t *testing.T) {
	r := &fakeProjectRequester{
		defaultServiceAccountPullSecrets: []string{"existing-secret", managedRegistrySecretName},
	}
	if err := removeProjectRegistryAccess(context.Background(), r, uuid.NewString(), "team-a"); err != nil {
		t.Fatalf("removeProjectRegistryAccess: %v", err)
	}
	if got, want := r.defaultServiceAccountPullSecrets, []string{"existing-secret"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("expected managed secret to be removed from serviceaccount, got %+v", got)
	}
}

func TestNormalizeRegistryAuthKey(t *testing.T) {
	cases := map[string]string{
		"https://registry.example.com/team/": "registry.example.com/team",
		"http://registry.example.com":        "registry.example.com",
		"registry.example.com/path":          "registry.example.com/path",
	}
	for in, want := range cases {
		if got := normalizeRegistryAuthKey(in); got != want {
			t.Fatalf("normalizeRegistryAuthKey(%q) = %q, want %q", in, got, want)
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

// --- per-project policy (migration 040) tests ----------------------------

// TestReconcileProjectNamespace_AppliesPSSLabels asserts the project-level
// pod_security_profile overrides the cluster template's PSS labels.
func TestReconcileProjectNamespace_AppliesPSSLabels(t *testing.T) {
	projectID := uuid.New()
	clusterID := uuid.New()
	q := &fakeProjectQuerier{
		project: sqlc.Project{
			ID:                 projectID,
			ClusterID:          clusterID,
			PodSecurityProfile: "restricted",
		},
		registryConfigErr: errors.New("no rows in result set"),
		// Template would otherwise set baseline — confirm the project value wins.
		defaultTemplate: sqlc.PodSecurityTemplate{
			EnforceLevel:   "baseline",
			EnforceVersion: "v1.29",
			AuditLevel:     "baseline",
			WarnLevel:      "baseline",
		},
	}
	r := &fakeProjectRequester{}
	if err := reconcileProjectNamespace(context.Background(), q, r, q.project, clusterID, "team-a"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if len(r.calls) == 0 || r.calls[0].method != http.MethodPatch {
		t.Fatalf("expected namespace label PATCH as first call, got %+v", r.calls)
	}
	var nsPatch map[string]any
	if err := json.Unmarshal(r.calls[0].body, &nsPatch); err != nil {
		t.Fatalf("unmarshal namespace patch: %v", err)
	}
	labels := nsPatch["metadata"].(map[string]any)["labels"].(map[string]any)
	wantLabels := map[string]string{
		"pod-security.kubernetes.io/enforce":         "restricted",
		"pod-security.kubernetes.io/enforce-version": "latest",
		"pod-security.kubernetes.io/audit":           "restricted",
		"pod-security.kubernetes.io/audit-version":   "latest",
		"pod-security.kubernetes.io/warn":            "restricted",
		"pod-security.kubernetes.io/warn-version":    "latest",
	}
	for k, want := range wantLabels {
		got, _ := labels[k].(string)
		if got != want {
			t.Errorf("label %q = %q, want %q (full set: %+v)", k, got, want, labels)
		}
	}
	// Project label must remain alongside.
	if labels[projectNamespaceLabelKey] != projectID.String() {
		t.Errorf("expected project-id label preserved, got %+v", labels)
	}
}

// TestReconcileProjectNamespace_AppliesResourceQuota asserts that with any
// non-empty quota field, an SSA call is dispatched against the project quota
// CR with the rendered spec.hard map.
func TestReconcileProjectNamespace_AppliesResourceQuota(t *testing.T) {
	projectID := uuid.New()
	clusterID := uuid.New()
	q := &fakeProjectQuerier{
		project: sqlc.Project{
			ID:                       projectID,
			ClusterID:                clusterID,
			PodSecurityProfile:       "baseline",
			ResourceQuotaCpuLimit:    "4",
			ResourceQuotaMemoryLimit: "8Gi",
			ResourceQuotaPodCount:    20,
		},
		registryConfigErr: errors.New("no rows in result set"),
	}
	r := &fakeProjectRequester{}
	if err := reconcileProjectNamespace(context.Background(), q, r, q.project, clusterID, "team-a"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var projectQuotaCall *fakeCall
	for i := range r.calls {
		c := &r.calls[i]
		if c.method == http.MethodPatch && strings.Contains(c.path, "/resourcequotas/"+managedProjectQuotaName) {
			projectQuotaCall = c
			break
		}
	}
	if projectQuotaCall == nil {
		t.Fatalf("expected SSA apply on %s, got calls: %+v", managedProjectQuotaName, r.calls)
	}
	var manifest map[string]any
	if err := json.Unmarshal(projectQuotaCall.body, &manifest); err != nil {
		t.Fatalf("unmarshal project quota manifest: %v", err)
	}
	if manifest["kind"] != "ResourceQuota" {
		t.Errorf("kind: got %v, want ResourceQuota", manifest["kind"])
	}
	meta := manifest["metadata"].(map[string]any)
	if meta["name"] != managedProjectQuotaName {
		t.Errorf("name: got %v, want %s", meta["name"], managedProjectQuotaName)
	}
	mlabels := meta["labels"].(map[string]any)
	if mlabels["app.kubernetes.io/managed-by"] != "astronomer" {
		t.Errorf("managed-by label wrong: %+v", mlabels)
	}
	if mlabels[projectPolicyLabelKey] != projectID.String() {
		t.Errorf("project id label wrong: %+v", mlabels)
	}
	hard := manifest["spec"].(map[string]any)["hard"].(map[string]any)
	if hard["limits.cpu"] != "4" {
		t.Errorf("limits.cpu: got %v, want 4", hard["limits.cpu"])
	}
	if hard["limits.memory"] != "8Gi" {
		t.Errorf("limits.memory: got %v, want 8Gi", hard["limits.memory"])
	}
	if hard["pods"] != "20" {
		t.Errorf("pods: got %v, want 20", hard["pods"])
	}
}

// TestReconcileProjectNamespace_SkipsQuotaWhenAllEmpty asserts that empty
// policy columns produce NO ResourceQuota apply — only a defensive DELETE so
// any stale object is reaped — preventing the empty-Quota-bans-everything
// foot-gun called out in the spec.
func TestReconcileProjectNamespace_SkipsQuotaWhenAllEmpty(t *testing.T) {
	clusterID := uuid.New()
	q := &fakeProjectQuerier{
		project: sqlc.Project{
			ID:                 uuid.New(),
			ClusterID:          clusterID,
			PodSecurityProfile: "baseline",
			// cpu/memory/pods left empty/zero on purpose.
		},
		registryConfigErr: errors.New("no rows in result set"),
	}
	r := &fakeProjectRequester{}
	if err := reconcileProjectNamespace(context.Background(), q, r, q.project, clusterID, "team-a"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	for _, c := range r.calls {
		if c.method == http.MethodPatch && strings.Contains(c.path, "/resourcequotas/"+managedProjectQuotaName) {
			t.Fatalf("expected NO apply on %s when all quota fields empty, got %+v", managedProjectQuotaName, c)
		}
	}
	// We DO expect a defensive DELETE so that a previously-set quota gets
	// cleaned up when an admin clears the fields.
	sawDelete := false
	for _, c := range r.calls {
		if c.method == http.MethodDelete && strings.Contains(c.path, "/resourcequotas/"+managedProjectQuotaName) {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("expected defensive DELETE on %s, calls=%+v", managedProjectQuotaName, r.calls)
	}
}

func TestRenderProjectResourceQuota_OnlyIncludesSetFields(t *testing.T) {
	project := sqlc.Project{
		ID:                       uuid.New(),
		PodSecurityProfile:       "baseline",
		ResourceQuotaCpuLimit:    "2",
		ResourceQuotaMemoryLimit: "", // intentionally unset
		ResourceQuotaPodCount:    0,  // intentionally unset
	}
	got := renderProjectResourceQuota("team-a", project)
	hard := got["spec"].(map[string]any)["hard"].(map[string]any)
	if hard["limits.cpu"] != "2" {
		t.Errorf("expected limits.cpu=2, got %+v", hard)
	}
	if _, ok := hard["limits.memory"]; ok {
		t.Errorf("expected no limits.memory entry, got %+v", hard)
	}
	if _, ok := hard["pods"]; ok {
		t.Errorf("expected no pods entry, got %+v", hard)
	}
}

func TestNormalizePodSecurityProfile(t *testing.T) {
	cases := map[string]string{
		"":           "privileged",
		"  ":         "privileged",
		"BASELINE":   "baseline",
		"baseline":   "baseline",
		"restricted": "restricted",
		"privileged": "privileged",
		"banana":     "privileged",
	}
	for in, want := range cases {
		if got := normalizePodSecurityProfile(in); got != want {
			t.Errorf("normalizePodSecurityProfile(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHasProjectQuotaPolicy(t *testing.T) {
	if hasProjectQuotaPolicy(sqlc.Project{}) {
		t.Error("empty project should not have policy")
	}
	if !hasProjectQuotaPolicy(sqlc.Project{ResourceQuotaCpuLimit: "2"}) {
		t.Error("cpu set should count")
	}
	if !hasProjectQuotaPolicy(sqlc.Project{ResourceQuotaMemoryLimit: "8Gi"}) {
		t.Error("memory set should count")
	}
	if !hasProjectQuotaPolicy(sqlc.Project{ResourceQuotaPodCount: 1}) {
		t.Error("pods set should count")
	}
}

// pgtypePinned silences the import without requiring an active reference;
// the production code uses pgtype.Timestamptz and tests will too once we
// extend coverage to last_reconciled_at windows. Kept as a vetted hook.
var pgtypePinned = pgtype.Timestamptz{}

var _ = pgtypePinned
var _ = time.Second
