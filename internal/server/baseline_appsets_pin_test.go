package server

import (
	"context"
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"

	"github.com/alphabravocompany/astronomer-go/internal/baseline"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func newBaselineAppSetFake() *fake.FakeDynamicClient {
	return fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		argocdApplicationSetGVR: "ApplicationSetList",
	})
}

func baselineAppSetRevision(t *testing.T, dyn *fake.FakeDynamicClient, name string) string {
	t.Helper()
	obj, err := dyn.Resource(argocdApplicationSetGVR).Namespace(localArgoNamespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get applicationset %s: %v", name, err)
	}
	revision, found, err := unstructured.NestedString(obj.Object, "spec", "template", "spec", "source", "targetRevision")
	if err != nil || !found {
		t.Fatalf("targetRevision on %s: found=%v err=%v", name, found, err)
	}
	return revision
}

func registryChartVersion(t *testing.T, slug string) string {
	t.Helper()
	for _, c := range baseline.Registry {
		if c.Slug == slug {
			return c.ChartVersion
		}
	}
	t.Fatalf("registry has no component %q", slug)
	return ""
}

// TestBaselineApplicationSetsPinChartVersion is the core regression fence.
// Before the fix every generated Application carried targetRevision "*", so an
// upstream publish of kube-state-metrics or prometheus-node-exporter reached
// every adopted cluster within one 30s reconcile tick, with prune on.
func TestBaselineApplicationSetsPinChartVersion(t *testing.T) {
	dyn := newBaselineAppSetFake()
	if err := ensureBaselineApplicationSets(context.Background(), dyn, baselineAppSetQuerierStub{}); err != nil {
		t.Fatalf("ensureBaselineApplicationSets: %v", err)
	}
	for slug, appSetName := range map[string]string{
		"kube-state-metrics":       "astronomer-baseline-kube-state-metrics",
		"prometheus-node-exporter": "astronomer-baseline-node-exporter",
	} {
		got := baselineAppSetRevision(t, dyn, appSetName)
		if got == baselineFloatingChartVersion {
			t.Fatalf("%s: targetRevision is still the floating %q", appSetName, got)
		}
		if want := registryChartVersion(t, slug); got != want {
			t.Fatalf("%s: targetRevision = %q, want the registry pin %q", appSetName, got, want)
		}
	}
}

// TestBaselineChartVersionOperatorOverride covers the whole precedence chain
// out of the cluster_tools catalog row, which is the operator-facing pin.
func TestBaselineChartVersionOperatorOverride(t *testing.T) {
	pinned := registryChartVersion(t, "kube-state-metrics")
	cases := []struct {
		name              string
		charts            string
		versionConstraint string
		want              string
	}{
		{
			name:   "chart version wins",
			charts: `[{"chart_name":"kube-state-metrics","repo_url":"https://charts.example.test/ksm","version":"5.15.2","order":0}]`,
			want:   "5.15.2",
		},
		{
			name:              "chart version beats version_constraint",
			charts:            `[{"chart_name":"kube-state-metrics","repo_url":"https://charts.example.test/ksm","version":"5.15.2","order":0}]`,
			versionConstraint: "5.16.0",
			want:              "5.15.2",
		},
		{
			// version_constraint is the column the Tools-view install path
			// already honors, so one row now means one version on both paths.
			name:              "version_constraint is the tool-wide fallback",
			charts:            `[{"chart_name":"kube-state-metrics","repo_url":"https://charts.example.test/ksm","order":0}]`,
			versionConstraint: "5.16.0",
			want:              "5.16.0",
		},
		{
			// ArgoCD reads targetRevision as a constraint, not an exact version.
			name:              "semver range is a legal pin",
			versionConstraint: ">=5.16.0 <6.0.0",
			want:              ">=5.16.0 <6.0.0",
		},
		{
			// A typo must not silently become "latest" — fall through to the
			// compiled-in pin, which is the whole point of having a floor.
			name:              "unparseable pin falls back to the registry",
			versionConstraint: "not-a-version",
			want:              pinned,
		},
		{
			name:              "explicit star is refused without the opt-in setting",
			versionConstraint: baselineFloatingChartVersion,
			want:              pinned,
		},
		{
			name: "blank row keeps the registry pin",
			want: pinned,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool := sqlc.ClusterTool{
				Slug:              "kube-state-metrics",
				VersionConstraint: tc.versionConstraint,
				IsEnabled:         true,
			}
			if tc.charts != "" {
				tool.Charts = json.RawMessage(tc.charts)
			}
			dyn := newBaselineAppSetFake()
			q := baselineAppSetQuerierStub{tools: map[string]sqlc.ClusterTool{"kube-state-metrics": tool}}
			if err := ensureBaselineApplicationSets(context.Background(), dyn, q); err != nil {
				t.Fatalf("ensureBaselineApplicationSets: %v", err)
			}
			if got := baselineAppSetRevision(t, dyn, "astronomer-baseline-kube-state-metrics"); got != tc.want {
				t.Fatalf("targetRevision = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBaselineFloatingChartVersionNeedsOptIn is the escape hatch: operators who
// deliberately want to track upstream can still have it, but only by saying so.
func TestBaselineFloatingChartVersionNeedsOptIn(t *testing.T) {
	dyn := newBaselineAppSetFake()
	q := baselineAppSetQuerierStub{
		settings: map[string]json.RawMessage{
			platformSettingBaselineUnpinnedCharts: json.RawMessage(`true`),
		},
		tools: map[string]sqlc.ClusterTool{
			"kube-state-metrics": {
				Slug:              "kube-state-metrics",
				VersionConstraint: baselineFloatingChartVersion,
				IsEnabled:         true,
			},
		},
	}
	if err := ensureBaselineApplicationSets(context.Background(), dyn, q); err != nil {
		t.Fatalf("ensureBaselineApplicationSets: %v", err)
	}
	if got := baselineAppSetRevision(t, dyn, "astronomer-baseline-kube-state-metrics"); got != baselineFloatingChartVersion {
		t.Fatalf("opted-in targetRevision = %q, want %q", got, baselineFloatingChartVersion)
	}
	// The setting only permits "*"; it does not force it onto components whose
	// row names a version.
	if got := baselineAppSetRevision(t, dyn, "astronomer-baseline-node-exporter"); got != registryChartVersion(t, "prometheus-node-exporter") {
		t.Fatalf("node-exporter targetRevision = %q, want the registry pin", got)
	}
}

// TestEnsureBaselineApplicationSetsRepinsExistingFloatingSet is the upgrade
// path, and the reason this change needs no data migration for the ArgoCD
// objects: every reconcile tick does a Get + full Update, so an ApplicationSet
// created by a pre-fix build with targetRevision "*" is rewritten with the pin
// on the first tick after upgrade. Without this the fix would protect only
// green-field installs.
func TestEnsureBaselineApplicationSetsRepinsExistingFloatingSet(t *testing.T) {
	dyn := newBaselineAppSetFake()
	ctx := context.Background()
	res := dyn.Resource(argocdApplicationSetGVR).Namespace(localArgoNamespace)

	// Stand in for what a pre-fix install left behind.
	legacy := baselineApplicationSetObject(baselineApplicationSetComponent{
		ApplicationSetName: "astronomer-baseline-kube-state-metrics",
		ApplicationPrefix:  "astronomer-ksm",
		Slug:               "kube-state-metrics",
		ChartName:          "kube-state-metrics",
		RepoURL:            "https://prometheus-community.github.io/helm-charts",
		ChartVersion:       baselineFloatingChartVersion,
		Namespace:          "astronomer-monitoring",
	}, nil)
	if _, err := res.Create(ctx, legacy, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed legacy applicationset: %v", err)
	}
	if got := baselineAppSetRevision(t, dyn, "astronomer-baseline-kube-state-metrics"); got != baselineFloatingChartVersion {
		t.Fatalf("seeded targetRevision = %q, want %q", got, baselineFloatingChartVersion)
	}

	if err := ensureBaselineApplicationSets(ctx, dyn, baselineAppSetQuerierStub{}); err != nil {
		t.Fatalf("ensureBaselineApplicationSets: %v", err)
	}
	if got, want := baselineAppSetRevision(t, dyn, "astronomer-baseline-kube-state-metrics"), registryChartVersion(t, "kube-state-metrics"); got != want {
		t.Fatalf("targetRevision after reconcile = %q, want %q", got, want)
	}
}

// TestEnsureBaselineApplicationSetsRejectsUnpinnedComponent proves the
// generator refuses to emit an Application it cannot pin rather than falling
// back to "*". Unreachable from operator data (resolveBaselineChartVersion
// always reaches the compiled-in floor); this guards a future registry entry
// that forgets its pin.
func TestEnsureBaselineApplicationSetsRejectsUnpinnedComponent(t *testing.T) {
	original := fallbackBaselineApplicationSetComponents
	t.Cleanup(func() { fallbackBaselineApplicationSetComponents = original })

	fallbackBaselineApplicationSetComponents = []baselineApplicationSetComponent{{
		ApplicationSetName: "astronomer-baseline-unpinned",
		ApplicationPrefix:  "astronomer-unpinned",
		Slug:               "unpinned",
		ChartName:          "unpinned",
		RepoURL:            "https://charts.example.test/unpinned",
		Namespace:          "astronomer-monitoring",
		DefaultEnabled:     true,
	}}
	dyn := newBaselineAppSetFake()
	err := ensureBaselineApplicationSets(context.Background(), dyn, baselineAppSetQuerierStub{})
	if err == nil {
		t.Fatal("expected an error for a component with no pinned chart version")
	}
	items, listErr := dyn.Resource(argocdApplicationSetGVR).Namespace(localArgoNamespace).List(context.Background(), metav1.ListOptions{})
	if listErr != nil {
		t.Fatalf("list applicationsets: %v", listErr)
	}
	if len(items.Items) != 0 {
		t.Fatalf("unpinned component was written anyway: %d appsets", len(items.Items))
	}
}
