package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type baselineAppSetQuerierStub struct {
	tools     map[string]sqlc.ClusterTool
	settings  map[string]json.RawMessage
	decisions []sqlc.ArgocdBaselineOwnershipDecision
}

func (q baselineAppSetQuerierStub) GetToolBySlug(_ context.Context, slug string) (sqlc.ClusterTool, error) {
	tool, ok := q.tools[slug]
	if !ok {
		return sqlc.ClusterTool{}, pgx.ErrNoRows
	}
	return tool, nil
}

func (q baselineAppSetQuerierStub) ListArgoCDBaselineOwnershipDecisionsByDecision(_ context.Context, decision string) ([]sqlc.ArgocdBaselineOwnershipDecision, error) {
	out := []sqlc.ArgocdBaselineOwnershipDecision{}
	for _, d := range q.decisions {
		if d.Decision == decision {
			out = append(out, d)
		}
	}
	return out, nil
}

func (q baselineAppSetQuerierStub) GetPlatformSetting(_ context.Context, key string) (sqlc.PlatformSetting, error) {
	value, ok := q.settings[key]
	if !ok {
		return sqlc.PlatformSetting{}, pgx.ErrNoRows
	}
	return sqlc.PlatformSetting{Key: key, Value: value}, nil
}

func TestEnsureBaselineApplicationSetsCreatesOwnedClusterGeneratorSets(t *testing.T) {
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		argocdApplicationSetGVR: "ApplicationSetList",
	})
	q := baselineAppSetQuerierStub{
		tools: map[string]sqlc.ClusterTool{
			"kube-state-metrics": {
				Slug:      "kube-state-metrics",
				Charts:    json.RawMessage(`[{"chart_name":"kube-state-metrics","repo_url":"https://charts.example.test/ksm","namespace":"observability","order":0}]`),
				Presets:   json.RawMessage(`{"default":"replicas: 2\n"}`),
				IsEnabled: true,
			},
		},
	}

	if err := ensureBaselineApplicationSets(context.Background(), dyn, q); err != nil {
		t.Fatalf("ensureBaselineApplicationSets: %v", err)
	}
	items, err := dyn.Resource(argocdApplicationSetGVR).Namespace(localArgoNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list applicationsets: %v", err)
	}
	// Baseline auto-manages only the two metrics exporters.
	if len(items.Items) != 2 {
		t.Fatalf("applicationset count = %d, want 2", len(items.Items))
	}
	ksm := findUnstructuredByName(items.Items, "astronomer-baseline-kube-state-metrics")
	if ksm == nil {
		t.Fatal("astronomer-baseline-kube-state-metrics not created")
	}
	generators, found, err := unstructured.NestedSlice(ksm.Object, "spec", "generators")
	if err != nil || !found || len(generators) != 1 {
		t.Fatalf("generators found=%v len=%d err=%v", found, len(generators), err)
	}
	generator := generators[0].(map[string]any)
	clusters := generator["clusters"].(map[string]any)
	selector := clusters["selector"].(map[string]any)
	labels := selector["matchLabels"].(map[string]any)
	if labels[argoCDManagedByLabelKey] != argoCDManagedByLabelValue {
		t.Fatalf("selector managed-by = %v, want %q", labels[argoCDManagedByLabelKey], argoCDManagedByLabelValue)
	}
	if labels[argoCDIsLocalLabelKey] != "false" {
		t.Fatalf("selector is-local = %v, want false", labels[argoCDIsLocalLabelKey])
	}
	appSetLabels := ksm.GetLabels()
	if appSetLabels[baselineApplicationSetTargetLabel] != baselineTargetAdoptedClusters {
		t.Fatalf("applicationset target label = %q, want %q", appSetLabels[baselineApplicationSetTargetLabel], baselineTargetAdoptedClusters)
	}
	if appSetLabels[baselineApplicationSetSyncPhaseLabel] != string(baselineSyncPhaseWorkloads) {
		t.Fatalf("applicationset sync phase label = %q, want %q", appSetLabels[baselineApplicationSetSyncPhaseLabel], baselineSyncPhaseWorkloads)
	}
	// DB tool override flows into the rendered source.
	repo, _, _ := unstructured.NestedString(ksm.Object, "spec", "template", "spec", "source", "repoURL")
	if repo != "https://charts.example.test/ksm" {
		t.Fatalf("repoURL = %q", repo)
	}
	namespace, _, _ := unstructured.NestedString(ksm.Object, "spec", "template", "spec", "destination", "namespace")
	if namespace != "observability" {
		t.Fatalf("namespace = %q, want observability", namespace)
	}
	values, _, _ := unstructured.NestedString(ksm.Object, "spec", "template", "spec", "source", "helm", "values")
	if values != "replicas: 2\n" {
		t.Fatalf("helm values = %q", values)
	}
	ksmWave, _, _ := unstructured.NestedString(ksm.Object, "spec", "template", "metadata", "annotations", "argocd.argoproj.io/sync-wave")
	if ksmWave != "10" {
		t.Fatalf("ksm sync wave = %q, want 10", ksmWave)
	}
	if findUnstructuredByName(items.Items, "astronomer-baseline-node-exporter") == nil {
		t.Fatal("astronomer-baseline-node-exporter not created")
	}
	// Opt-in components are Tools-owned, never baseline appsets.
	for _, optIn := range []string{"astronomer-baseline-trivy", "astronomer-baseline-fluent-bit", "astronomer-baseline-ingress-nginx", "astronomer-baseline-cert-manager", "astronomer-baseline-gatekeeper"} {
		if findUnstructuredByName(items.Items, optIn) != nil {
			t.Fatalf("%s should not be a baseline appset", optIn)
		}
	}
}

func TestBaselineApplicationSetsUseSyncWaveStandards(t *testing.T) {
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		argocdApplicationSetGVR: "ApplicationSetList",
	})
	if err := ensureBaselineApplicationSets(context.Background(), dyn, baselineAppSetQuerierStub{}); err != nil {
		t.Fatalf("ensureBaselineApplicationSets: %v", err)
	}
	items, err := dyn.Resource(argocdApplicationSetGVR).Namespace(localArgoNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list applicationsets: %v", err)
	}

	expected := map[string]struct {
		phase baselineSyncPhase
		wave  string
	}{
		"astronomer-baseline-kube-state-metrics": {phase: baselineSyncPhaseWorkloads, wave: "10"},
		"astronomer-baseline-node-exporter":      {phase: baselineSyncPhaseWorkloads, wave: "10"},
	}
	if len(items.Items) != len(expected) {
		t.Fatalf("applicationset count = %d, want %d", len(items.Items), len(expected))
	}
	for name, want := range expected {
		appSet := findUnstructuredByName(items.Items, name)
		if appSet == nil {
			t.Fatalf("%s not created", name)
		}
		if got := appSet.GetLabels()[baselineApplicationSetSyncPhaseLabel]; got != string(want.phase) {
			t.Fatalf("%s sync phase label = %q, want %q", name, got, want.phase)
		}
		templatePhase, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "metadata", "labels", baselineApplicationSetSyncPhaseLabel)
		if templatePhase != string(want.phase) {
			t.Fatalf("%s template sync phase label = %q, want %q", name, templatePhase, want.phase)
		}
		templateWave, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "metadata", "annotations", "argocd.argoproj.io/sync-wave")
		if templateWave != want.wave {
			t.Fatalf("%s sync wave = %q, want %q", name, templateWave, want.wave)
		}
	}
}

func TestBaselineSyncWaveStandardsReserveAllLifecyclePhases(t *testing.T) {
	expected := map[baselineSyncPhase]int{
		baselineSyncPhaseNamespaces:  baselineSyncWaveNamespaces,
		baselineSyncPhaseCRDs:        baselineSyncWaveCRDs,
		baselineSyncPhaseOperators:   baselineSyncWaveOperators,
		baselineSyncPhasePolicies:    baselineSyncWavePolicies,
		baselineSyncPhaseWorkloads:   baselineSyncWaveWorkloads,
		baselineSyncPhaseHealthCheck: baselineSyncWaveHealthCheck,
	}
	for phase, wave := range expected {
		if got := baselineSyncWaveForPhase(phase); got != wave {
			t.Fatalf("%s wave = %d, want %d", phase, got, wave)
		}
	}
	if got := baselineSyncWaveForPhase("unknown"); got != baselineSyncWaveWorkloads {
		t.Fatalf("unknown phase wave = %d, want default workload wave %d", got, baselineSyncWaveWorkloads)
	}
}

func TestArgoCDManagePlatformBaselineEnabledDefaultsTrue(t *testing.T) {
	if !argoCDManagePlatformBaselineEnabled(context.Background(), baselineAppSetQuerierStub{}) {
		t.Fatal("missing setting should default to enabled")
	}
	if argoCDManagePlatformBaselineEnabled(context.Background(), baselineAppSetQuerierStub{
		settings: map[string]json.RawMessage{platformSettingArgoCDManageBaseline: json.RawMessage(`false`)},
	}) {
		t.Fatal("false setting should disable baseline ApplicationSets")
	}
}

func TestBaselineDefaultsExcludeOpinionatedInfra(t *testing.T) {
	newDyn := func() *fake.FakeDynamicClient {
		return fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
			argocdApplicationSetGVR: "ApplicationSetList",
		})
	}
	names := func(dyn *fake.FakeDynamicClient) map[string]bool {
		items, err := dyn.Resource(argocdApplicationSetGVR).Namespace(localArgoNamespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			t.Fatalf("list applicationsets: %v", err)
		}
		got := map[string]bool{}
		for i := range items.Items {
			got[items.Items[i].GetName()] = true
		}
		return got
	}

	// No settings → only the two metrics exporters ship by default.
	dyn := newDyn()
	if err := ensureBaselineApplicationSets(context.Background(), dyn, baselineAppSetQuerierStub{}); err != nil {
		t.Fatalf("ensureBaselineApplicationSets: %v", err)
	}
	got := names(dyn)
	for _, on := range []string{"astronomer-baseline-kube-state-metrics", "astronomer-baseline-node-exporter"} {
		if !got[on] {
			t.Errorf("default-on component %s missing", on)
		}
	}
	for _, off := range []string{"astronomer-baseline-trivy", "astronomer-baseline-fluent-bit", "astronomer-baseline-ingress-nginx", "astronomer-baseline-cert-manager", "astronomer-baseline-gatekeeper"} {
		if got[off] {
			t.Errorf("opt-in component %s should not ship by default, got created", off)
		}
	}

	// Explicit false disables a default-on exporter.
	dyn = newDyn()
	q := baselineAppSetQuerierStub{settings: map[string]json.RawMessage{
		platformSettingBaselineComponentPrefix + "kube-state-metrics": json.RawMessage(`false`),
	}}
	if err := ensureBaselineApplicationSets(context.Background(), dyn, q); err != nil {
		t.Fatalf("ensureBaselineApplicationSets: %v", err)
	}
	if names(dyn)["astronomer-baseline-kube-state-metrics"] {
		t.Error("kube-state-metrics disabled via setting should not be created")
	}
}

// selectorMatchExpressions returns the cluster generator's selector
// matchExpressions for an appset (nil if none).
func selectorMatchExpressions(t *testing.T, appSet *unstructured.Unstructured) []any {
	t.Helper()
	generators, found, err := unstructured.NestedSlice(appSet.Object, "spec", "generators")
	if err != nil || !found || len(generators) != 1 {
		t.Fatalf("generators found=%v len=%d err=%v", found, len(generators), err)
	}
	selector := generators[0].(map[string]any)["clusters"].(map[string]any)["selector"].(map[string]any)
	exprs, ok := selector["matchExpressions"].([]any)
	if !ok {
		return nil
	}
	return exprs
}

// findMatchExpr returns the matchExpression with the given key, or nil.
func findMatchExpr(exprs []any, key string) map[string]any {
	for _, e := range exprs {
		m, ok := e.(map[string]any)
		if ok && m["key"] == key {
			return m
		}
	}
	return nil
}

// assertProfilePreflight asserts the M9 profile filter is always present:
// In [operator, admin] on the agent-profile label.
func assertProfilePreflight(t *testing.T, exprs []any) {
	t.Helper()
	prof := findMatchExpr(exprs, argoCDAgentProfileLabelKey)
	if prof == nil || prof["operator"] != "In" {
		t.Fatalf("expected an In matchExpression on %s (M9 profile pre-flight), got %v", argoCDAgentProfileLabelKey, exprs)
	}
	vals := prof["values"].([]any)
	if len(vals) != 2 || vals[0] != "operator" || vals[1] != "admin" {
		t.Fatalf("profile In values = %v, want [operator admin]", vals)
	}
}

// TestEnsureBaselineApplicationSetsAdminPushUnbroken is constraint #1: with pull
// OFF (the default — ensureBaselineApplicationSets is only ever called when
// !cfg.PullReconcileEnabled) and NO ownership decisions, the baseline appset is
// generated exactly as today — the managed-by/is-local matchLabels and NO
// cluster-id matchExpressions clause. Covers matrix (a) and the no-decision/
// adopt default (d).
func TestEnsureBaselineApplicationSetsAdminPushUnbroken(t *testing.T) {
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		argocdApplicationSetGVR: "ApplicationSetList",
	})
	if err := ensureBaselineApplicationSets(context.Background(), dyn, baselineAppSetQuerierStub{}); err != nil {
		t.Fatalf("ensureBaselineApplicationSets: %v", err)
	}
	items, err := dyn.Resource(argocdApplicationSetGVR).Namespace(localArgoNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list applicationsets: %v", err)
	}
	if len(items.Items) != 2 {
		t.Fatalf("applicationset count = %d, want 2", len(items.Items))
	}
	for _, name := range []string{"astronomer-baseline-kube-state-metrics", "astronomer-baseline-node-exporter"} {
		appSet := findUnstructuredByName(items.Items, name)
		if appSet == nil {
			t.Fatalf("%s not created", name)
		}
		selector := appSet.Object["spec"].(map[string]any)["generators"].([]any)[0].(map[string]any)["clusters"].(map[string]any)["selector"].(map[string]any)
		labels := selector["matchLabels"].(map[string]any)
		if labels[argoCDManagedByLabelKey] != argoCDManagedByLabelValue || labels[argoCDIsLocalLabelKey] != "false" {
			t.Fatalf("%s matchLabels = %v", name, labels)
		}
		// M9: the profile pre-flight In [operator,admin] is always present (admin
		// clusters still match → admin-push unbroken). With no leave_local
		// decisions there is NO additional cluster-id NotIn expression.
		exprs := selectorMatchExpressions(t, appSet)
		assertProfilePreflight(t, exprs)
		if findMatchExpr(exprs, argoCDClusterIDLabelKey) != nil {
			t.Fatalf("%s must have NO cluster-id NotIn when no leave_local decisions exist, got %v", name, exprs)
		}
	}
}

// TestEnsureBaselineApplicationSetsExcludesLeaveLocal is matrix (c): a
// leave_local decision for (cluster, kube-state-metrics) appends a cluster-id
// NotIn matchExpression to ONLY that component's selector, excluding the cluster
// from the fan-out; node-exporter (no decision) is untouched.
func TestEnsureBaselineApplicationSetsExcludesLeaveLocal(t *testing.T) {
	clusterID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		argocdApplicationSetGVR: "ApplicationSetList",
	})
	q := baselineAppSetQuerierStub{
		decisions: []sqlc.ArgocdBaselineOwnershipDecision{
			{ClusterID: clusterID, ComponentSlug: "kube-state-metrics", Decision: "leave_local"},
		},
	}
	if err := ensureBaselineApplicationSets(context.Background(), dyn, q); err != nil {
		t.Fatalf("ensureBaselineApplicationSets: %v", err)
	}
	items, err := dyn.Resource(argocdApplicationSetGVR).Namespace(localArgoNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list applicationsets: %v", err)
	}
	ksm := findUnstructuredByName(items.Items, "astronomer-baseline-kube-state-metrics")
	if ksm == nil {
		t.Fatal("kube-state-metrics appset missing")
	}
	exprs := selectorMatchExpressions(t, ksm)
	// ksm: profile pre-flight (M9) + the leave_local cluster-id NotIn (H7).
	assertProfilePreflight(t, exprs)
	expr := findMatchExpr(exprs, argoCDClusterIDLabelKey)
	if expr == nil || expr["operator"] != "NotIn" {
		t.Fatalf("ksm missing cluster-id NotIn matchExpression, got %v", exprs)
	}
	values := expr["values"].([]any)
	if len(values) != 1 || values[0] != clusterID.String() {
		t.Fatalf("ksm NotIn values = %v, want [%s]", values, clusterID)
	}
	// node-exporter has no decision → profile pre-flight only, no cluster-id NotIn.
	ne := findUnstructuredByName(items.Items, "astronomer-baseline-node-exporter")
	if ne == nil {
		t.Fatal("node-exporter appset missing")
	}
	neExprs := selectorMatchExpressions(t, ne)
	assertProfilePreflight(t, neExprs)
	if findMatchExpr(neExprs, argoCDClusterIDLabelKey) != nil {
		t.Fatalf("node-exporter must have NO cluster-id NotIn, got %v", neExprs)
	}
}

// TestEnsureBaselineApplicationSetsIncludesAdoptAndReplace is matrix (d)+(e):
// adopt and replace decisions are NOT leave_local, so they never exclude the
// cluster — no matchExpressions are added.
func TestEnsureBaselineApplicationSetsIncludesAdoptAndReplace(t *testing.T) {
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		argocdApplicationSetGVR: "ApplicationSetList",
	})
	q := baselineAppSetQuerierStub{
		decisions: []sqlc.ArgocdBaselineOwnershipDecision{
			{ClusterID: uuid.MustParse("33333333-3333-3333-3333-333333333333"), ComponentSlug: "kube-state-metrics", Decision: "adopt"},
			{ClusterID: uuid.MustParse("44444444-4444-4444-4444-444444444444"), ComponentSlug: "prometheus-node-exporter", Decision: "replace"},
		},
	}
	if err := ensureBaselineApplicationSets(context.Background(), dyn, q); err != nil {
		t.Fatalf("ensureBaselineApplicationSets: %v", err)
	}
	items, err := dyn.Resource(argocdApplicationSetGVR).Namespace(localArgoNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list applicationsets: %v", err)
	}
	for _, name := range []string{"astronomer-baseline-kube-state-metrics", "astronomer-baseline-node-exporter"} {
		appSet := findUnstructuredByName(items.Items, name)
		if appSet == nil {
			t.Fatalf("%s not created", name)
		}
		// adopt/replace are not leave_local → profile pre-flight only, no NotIn.
		exprs := selectorMatchExpressions(t, appSet)
		assertProfilePreflight(t, exprs)
		if findMatchExpr(exprs, argoCDClusterIDLabelKey) != nil {
			t.Fatalf("%s adopt/replace must NOT add a cluster-id NotIn, got %v", name, exprs)
		}
	}
}

// TestBaselineAppHasResourcesFinalizer (L10): the generated Application template
// must carry the ArgoCD resources-finalizer so disabling a component (or
// excluding a cluster) cascades the prune to the actual workloads instead of
// orphaning them.
func TestBaselineAppHasResourcesFinalizer(t *testing.T) {
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		argocdApplicationSetGVR: "ApplicationSetList",
	})
	if err := ensureBaselineApplicationSets(context.Background(), dyn, baselineAppSetQuerierStub{}); err != nil {
		t.Fatalf("ensureBaselineApplicationSets: %v", err)
	}
	items, _ := dyn.Resource(argocdApplicationSetGVR).Namespace(localArgoNamespace).List(context.Background(), metav1.ListOptions{})
	appSet := findUnstructuredByName(items.Items, "astronomer-baseline-kube-state-metrics")
	if appSet == nil {
		t.Fatal("ksm appset missing")
	}
	tmplMeta := appSet.Object["spec"].(map[string]any)["template"].(map[string]any)["metadata"].(map[string]any)
	fins, _ := tmplMeta["finalizers"].([]any)
	found := false
	for _, f := range fins {
		if f == "resources-finalizer.argocd.argoproj.io" {
			found = true
		}
	}
	if !found {
		t.Fatalf("App template missing resources-finalizer (L10), got finalizers=%v", fins)
	}
}

// TestEnsureBaselineApplicationSetsProfilePreflight is the M9 assertion: the
// generator always filters on In [operator, admin] so viewer / namespace-*
// clusters (which 403 on baseline apply) never get a baseline App.
func TestEnsureBaselineApplicationSetsProfilePreflight(t *testing.T) {
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		argocdApplicationSetGVR: "ApplicationSetList",
	})
	if err := ensureBaselineApplicationSets(context.Background(), dyn, baselineAppSetQuerierStub{}); err != nil {
		t.Fatalf("ensureBaselineApplicationSets: %v", err)
	}
	items, _ := dyn.Resource(argocdApplicationSetGVR).Namespace(localArgoNamespace).List(context.Background(), metav1.ListOptions{})
	for _, name := range []string{"astronomer-baseline-kube-state-metrics", "astronomer-baseline-node-exporter"} {
		appSet := findUnstructuredByName(items.Items, name)
		if appSet == nil {
			t.Fatalf("%s not created", name)
		}
		assertProfilePreflight(t, selectorMatchExpressions(t, appSet))
	}
}

// TestRemoveBaselineApplicationSetsPrunesPushedAppsets is matrix (b): the
// pull-ON stand-down. After push previously created the baseline appsets, the
// teardown reconcileLocalArgoSelfManagement runs when PullReconcileEnabled is
// true prunes them all, so no server-pushed baseline App fans onto the pull
// cluster.
func TestRemoveBaselineApplicationSetsPrunesPushedAppsets(t *testing.T) {
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		argocdApplicationSetGVR: "ApplicationSetList",
	})
	if err := ensureBaselineApplicationSets(context.Background(), dyn, baselineAppSetQuerierStub{}); err != nil {
		t.Fatalf("ensureBaselineApplicationSets: %v", err)
	}
	// Teardown is idempotent: a second call (green-field pull, nothing to prune)
	// must also succeed.
	for i := 0; i < 2; i++ {
		if err := removeBaselineApplicationSets(context.Background(), dyn); err != nil {
			t.Fatalf("removeBaselineApplicationSets: %v", err)
		}
	}
	items, err := dyn.Resource(argocdApplicationSetGVR).Namespace(localArgoNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list applicationsets: %v", err)
	}
	if len(items.Items) != 0 {
		t.Fatalf("baseline appsets remain after teardown: %d", len(items.Items))
	}
}

func findUnstructuredByName(items []unstructured.Unstructured, name string) *unstructured.Unstructured {
	for i := range items {
		if items[i].GetName() == name {
			return &items[i]
		}
	}
	return nil
}
