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
	q := baselineAppSetQuerierStub{tools: map[string]sqlc.ClusterTool{
		"trivy-operator": {
			Slug:      "trivy-operator",
			Charts:    json.RawMessage(`[{"chart_name":"trivy-operator","repo_url":"https://charts.example.test/aqua","namespace":"security","order":0}]`),
			Presets:   json.RawMessage(`{"default":"operator:\n  scanJobTimeout: 10m\n"}`),
			IsEnabled: true,
		},
	}}

	if err := ensureBaselineApplicationSets(context.Background(), dyn, q); err != nil {
		t.Fatalf("ensureBaselineApplicationSets: %v", err)
	}
	items, err := dyn.Resource(argocdApplicationSetGVR).Namespace(localArgoNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list applicationsets: %v", err)
	}
	if len(items.Items) != 5 {
		t.Fatalf("applicationset count = %d, want 5", len(items.Items))
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

func findUnstructuredByName(items []unstructured.Unstructured, name string) *unstructured.Unstructured {
	for i := range items {
		if items[i].GetName() == name {
			return &items[i]
		}
	}
	return nil
}
