package airgap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const repoChart = "../../../deploy/chart"

func TestPlanAgainstRepositoryChart(t *testing.T) {
	t.Parallel()
	plan, err := BuildPlan(Inputs{ImagesFile: filepath.Join(repoChart, "images.txt"), ChartValues: filepath.Join(repoChart, "values.yaml"), Destination: "zot.astronomer.svc.cluster.local:5000"})
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]Item{}
	for _, item := range plan.Items {
		sources[item.Source] = item
		if !strings.HasPrefix(item.Target, "zot.astronomer.svc.cluster.local:5000/") {
			t.Errorf("item %s has non-local target %s", item.Source, item.Target)
		}
	}
	for _, want := range []string{
		"ghcr.io/alphabravo-oss/astronomer-go-server:",
		"ghcr.io/alphabravo-oss/astronomer-go-agent:",
		"ghcr.io/fluxcd/helm-controller@sha256:",
		"registry.k8s.io/kube-state-metrics/kube-state-metrics@sha256:",
		"ghcr.io/project-zot/zot:v2.1.22@sha256:",
		"busybox:",
	} {
		found := false
		for source := range sources {
			if strings.HasPrefix(source, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("plan lacks %s*", want)
		}
	}
	if item := sources["busybox:1.36"]; item.Target != "zot.astronomer.svc.cluster.local:5000/busybox:1.36" {
		t.Errorf("docker hub short name target = %q", item.Target)
	}
	for i := 1; i < len(plan.Items); i++ {
		if plan.Items[i-1].Source >= plan.Items[i].Source {
			t.Fatal("plan is not sorted/deduplicated")
		}
	}
}

func TestPlanIncludesReleaseManifestArtifacts(t *testing.T) {
	t.Parallel()
	digest := func(c string) string { return "sha256:" + strings.Repeat(c, 64) }
	manifest := map[string]any{
		"schema_version": 1,
		"release":        map[string]any{"version": "v2.0.1"},
		"astronomer": map[string]any{
			"chart":          map[string]any{"name": "chart", "kind": KindChart, "reference": "oci://ghcr.io/alphabravo-oss/charts/astronomer@" + digest("a")},
			"images":         []any{map[string]any{"name": "server", "kind": KindImage, "reference": "ghcr.io/alphabravo-oss/astronomer-go-server@" + digest("b")}},
			"runtime_images": []any{map[string]any{"reference": "docker.io/library/busybox@" + digest("c")}},
		},
		"flux":             map[string]any{"distribution": map[string]any{"kind": KindArtifact, "reference": "ghcr.io/alphabravo-oss/flux-distribution@" + digest("d")}},
		"built_in_bundles": map[string]any{"artifact": map[string]any{"kind": KindArtifact, "reference": "ghcr.io/alphabravo-oss/builtins@" + digest("e")}},
	}
	raw, _ := json.Marshal(manifest)
	path := filepath.Join(t.TempDir(), "release-manifest.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(Inputs{ReleaseManifest: path, Destination: "zot:5000"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Release != "v2.0.1" {
		t.Fatalf("release = %q", plan.Release)
	}
	kinds := map[string]string{}
	for _, item := range plan.Items {
		kinds[item.Target] = item.Kind
	}
	for target, kind := range map[string]string{
		"zot:5000/alphabravo-oss/charts/astronomer@" + digest("a"): KindChart,
		"zot:5000/alphabravo-oss/flux-distribution@" + digest("d"): KindArtifact,
		"zot:5000/alphabravo-oss/builtins@" + digest("e"):          KindArtifact,
		"zot:5000/busybox@" + digest("c"):                          KindImage,
	} {
		if kinds[target] != kind {
			t.Errorf("target %s kind = %q, want %q", target, kinds[target], kind)
		}
	}
}
