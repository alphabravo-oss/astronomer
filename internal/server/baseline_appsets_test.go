package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type baselineAppSetQuerierStub struct {
	tools    map[string]sqlc.ClusterTool
	settings map[string]json.RawMessage
}

func (q baselineAppSetQuerierStub) GetToolBySlug(_ context.Context, slug string) (sqlc.ClusterTool, error) {
	tool, ok := q.tools[slug]
	if !ok {
		return sqlc.ClusterTool{}, pgx.ErrNoRows
	}
	return tool, nil
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

func findUnstructuredByName(items []unstructured.Unstructured, name string) *unstructured.Unstructured {
	for i := range items {
		if items[i].GetName() == name {
			return &items[i]
		}
	}
	return nil
}
