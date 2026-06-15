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
			"trivy-operator": {
				Slug:      "trivy-operator",
				Charts:    json.RawMessage(`[{"chart_name":"trivy-operator","repo_url":"https://charts.example.test/aqua","namespace":"security","order":0}]`),
				Presets:   json.RawMessage(`{"default":"operator:\n  scanJobTimeout: 10m\n"}`),
				IsEnabled: true,
			},
		},
		// Only ksm + node-exporter are default-on; enable the other five so this
		// test exercises the full 7-component render (labels, generators, waves).
		settings: map[string]json.RawMessage{
			platformSettingBaselineComponentPrefix + "trivy-operator": json.RawMessage(`true`),
			platformSettingBaselineComponentPrefix + "fluent-bit":     json.RawMessage(`true`),
			platformSettingBaselineComponentPrefix + "ingress-nginx":  json.RawMessage(`true`),
			platformSettingBaselineComponentPrefix + "cert-manager":   json.RawMessage(`true`),
			platformSettingBaselineComponentPrefix + "gatekeeper":     json.RawMessage(`true`),
		},
	}

	if err := ensureBaselineApplicationSets(context.Background(), dyn, q); err != nil {
		t.Fatalf("ensureBaselineApplicationSets: %v", err)
	}
	items, err := dyn.Resource(argocdApplicationSetGVR).Namespace(localArgoNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list applicationsets: %v", err)
	}
	if len(items.Items) != 7 {
		t.Fatalf("applicationset count = %d, want 7", len(items.Items))
	}
	trivy := findUnstructuredByName(items.Items, "astronomer-baseline-trivy")
	if trivy == nil {
		t.Fatal("astronomer-baseline-trivy not created")
	}
	generators, found, err := unstructured.NestedSlice(trivy.Object, "spec", "generators")
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
	appSetLabels := trivy.GetLabels()
	if appSetLabels[baselineApplicationSetTargetLabel] != baselineTargetAdoptedClusters {
		t.Fatalf("applicationset target label = %q, want %q", appSetLabels[baselineApplicationSetTargetLabel], baselineTargetAdoptedClusters)
	}
	if appSetLabels[baselineApplicationSetSyncPhaseLabel] != string(baselineSyncPhaseHealthCheck) {
		t.Fatalf("applicationset sync phase label = %q, want %q", appSetLabels[baselineApplicationSetSyncPhaseLabel], baselineSyncPhaseHealthCheck)
	}
	repo, _, _ := unstructured.NestedString(trivy.Object, "spec", "template", "spec", "source", "repoURL")
	if repo != "https://charts.example.test/aqua" {
		t.Fatalf("repoURL = %q", repo)
	}
	namespace, _, _ := unstructured.NestedString(trivy.Object, "spec", "template", "spec", "destination", "namespace")
	if namespace != "security" {
		t.Fatalf("namespace = %q, want security", namespace)
	}
	values, _, _ := unstructured.NestedString(trivy.Object, "spec", "template", "spec", "source", "helm", "values")
	if values != "operator:\n  scanJobTimeout: 10m\n" {
		t.Fatalf("helm values = %q", values)
	}
	trivyWave, _, _ := unstructured.NestedString(trivy.Object, "spec", "template", "metadata", "annotations", "argocd.argoproj.io/sync-wave")
	if trivyWave != "30" {
		t.Fatalf("trivy sync wave = %q, want 30", trivyWave)
	}
	ingress := findUnstructuredByName(items.Items, "astronomer-baseline-ingress-nginx")
	if ingress == nil {
		t.Fatal("astronomer-baseline-ingress-nginx not created")
	}
	ingressRepo, _, _ := unstructured.NestedString(ingress.Object, "spec", "template", "spec", "source", "repoURL")
	if ingressRepo != "https://kubernetes.github.io/ingress-nginx" {
		t.Fatalf("ingress repoURL = %q", ingressRepo)
	}
	certManager := findUnstructuredByName(items.Items, "astronomer-baseline-cert-manager")
	if certManager == nil {
		t.Fatal("astronomer-baseline-cert-manager not created")
	}
	certManagerWave, _, _ := unstructured.NestedString(certManager.Object, "spec", "template", "metadata", "annotations", "argocd.argoproj.io/sync-wave")
	if certManagerWave != "-30" {
		t.Fatalf("cert-manager sync wave = %q, want -30", certManagerWave)
	}
	gatekeeper := findUnstructuredByName(items.Items, "astronomer-baseline-gatekeeper")
	if gatekeeper == nil {
		t.Fatal("astronomer-baseline-gatekeeper not created")
	}
	gatekeeperRepo, _, _ := unstructured.NestedString(gatekeeper.Object, "spec", "template", "spec", "source", "repoURL")
	if gatekeeperRepo != "https://open-policy-agent.github.io/gatekeeper/charts" {
		t.Fatalf("gatekeeper repoURL = %q", gatekeeperRepo)
	}
	gatekeeperWave, _, _ := unstructured.NestedString(gatekeeper.Object, "spec", "template", "metadata", "annotations", "argocd.argoproj.io/sync-wave")
	if gatekeeperWave != "-10" {
		t.Fatalf("gatekeeper sync wave = %q, want -10", gatekeeperWave)
	}
}

func TestBaselineApplicationSetsUseSyncWaveStandards(t *testing.T) {
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		argocdApplicationSetGVR: "ApplicationSetList",
	})
	q := baselineAppSetQuerierStub{settings: map[string]json.RawMessage{
		platformSettingBaselineComponentPrefix + "trivy-operator": json.RawMessage(`true`),
		platformSettingBaselineComponentPrefix + "fluent-bit":     json.RawMessage(`true`),
		platformSettingBaselineComponentPrefix + "ingress-nginx":  json.RawMessage(`true`),
		platformSettingBaselineComponentPrefix + "cert-manager":   json.RawMessage(`true`),
		platformSettingBaselineComponentPrefix + "gatekeeper":     json.RawMessage(`true`),
	}}
	if err := ensureBaselineApplicationSets(context.Background(), dyn, q); err != nil {
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
		"astronomer-baseline-cert-manager":       {phase: baselineSyncPhaseCRDs, wave: "-30"},
		"astronomer-baseline-ingress-nginx":      {phase: baselineSyncPhaseOperators, wave: "-20"},
		"astronomer-baseline-kube-state-metrics": {phase: baselineSyncPhaseWorkloads, wave: "10"},
		"astronomer-baseline-node-exporter":      {phase: baselineSyncPhaseWorkloads, wave: "10"},
		"astronomer-baseline-fluent-bit":         {phase: baselineSyncPhaseWorkloads, wave: "10"},
		"astronomer-baseline-gatekeeper":         {phase: baselineSyncPhasePolicies, wave: "-10"},
		"astronomer-baseline-trivy":              {phase: baselineSyncPhaseHealthCheck, wave: "30"},
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
