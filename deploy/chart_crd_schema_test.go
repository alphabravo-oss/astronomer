package deploy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func readChartTemplate(t *testing.T, name string) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	path := filepath.Join(filepath.Dir(here), "chart", "templates", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestManagementCRDTemplatesDefineRetainedGenericKinds(t *testing.T) {
	tests := []struct {
		file      string
		name      string
		kind      string
		shortName string
		specBits  []string
	}{
		{
			file: "crd-cluster.yaml", name: "clusters.management.astronomer.io",
			kind: "Cluster", shortName: "astrocluster",
			specBits: []string{"profileRef:", "privilegeProfile:", "adoptionPolicy:"},
		},
		{
			file: "crd-project.yaml", name: "projects.management.astronomer.io",
			kind: "Project", shortName: "astroproject",
			specBits: []string{"clusters:", "podSecurityProfile:", "resourceQuota:"},
		},
		{
			file: "crd-agentprofile.yaml", name: "agentprofiles.management.astronomer.io",
			kind: "AgentProfile", shortName: "astroagentprofile",
			specBits: []string{"privilegeProfile:", "allowedRules:", "hostAccess:", "networkEgress:"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			body := readChartTemplate(t, tt.file)
			for _, want := range []string{
				"{{- if .Values.crds.enabled }}", "kind: CustomResourceDefinition",
				"name: " + tt.name, "kind: " + tt.kind, "- " + tt.shortName,
				`"helm.sh/resource-policy": keep`, "subresources:", "status: {}",
				"observedGeneration:", "conditions:", "lastTransitionTime:",
			} {
				if !strings.Contains(body, want) {
					t.Fatalf("%s missing %q", tt.file, want)
				}
			}
			for _, want := range tt.specBits {
				if !strings.Contains(body, want) {
					t.Fatalf("%s missing spec fragment %q", tt.file, want)
				}
			}
		})
	}
}

func TestManagementCRDRBACIncludesOnlyRetainedGenericKinds(t *testing.T) {
	body := readChartTemplate(t, "crd-rbac.yaml")
	for _, resource := range []string{
		"clusters", "projects", "agentprofiles",
		"clusters/status", "projects/status", "agentprofiles/status",
		"clusters/finalizers", "projects/finalizers", "agentprofiles/finalizers",
	} {
		if !strings.Contains(body, "- "+resource) {
			t.Fatalf("crd-rbac.yaml missing resource %q", resource)
		}
	}
	for _, removed := range []string{
		"clusterbaselines", "componentbundles", "gitopstargets",
		"application" + "sets", "application" + "s",
	} {
		if strings.Contains(strings.ToLower(body), removed) {
			t.Fatalf("crd-rbac.yaml retains removed resource %q", removed)
		}
	}
}

func TestClusterCRDTemplateIncludesAgentProfileRef(t *testing.T) {
	body := readChartTemplate(t, "crd-cluster.yaml")
	for _, want := range []string{"profileRef:", "namespace-viewer", "namespace-operator", "custom"} {
		if !strings.Contains(body, want) {
			t.Fatalf("crd-cluster.yaml missing %q", want)
		}
	}
}

func TestCRDControllerServerEnvHasOnlyGenericSettings(t *testing.T) {
	body := readChartTemplate(t, "server-deployment.yaml")
	for _, want := range []string{"CRD_ENABLED", "CRD_WATCH_NAMESPACE"} {
		if !strings.Contains(body, want) {
			t.Fatalf("server-deployment.yaml missing %q", want)
		}
	}
	for _, removed := range []string{"CRD_" + "ARGO_NAMESPACE", ".Values.crds." + "argoNamespace"} {
		if strings.Contains(body, removed) {
			t.Fatalf("server-deployment.yaml retains removed setting %q", removed)
		}
	}
}
