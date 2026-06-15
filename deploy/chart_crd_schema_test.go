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

func TestManagementCRDTemplatesDefinePlannedKinds(t *testing.T) {
	tests := []struct {
		file      string
		name      string
		kind      string
		shortName string
		finalizer string
		specBits  []string
	}{
		{
			file:      "crd-clusterbaseline.yaml",
			name:      "clusterbaselines.management.astronomer.io",
			kind:      "ClusterBaseline",
			shortName: "astrobaseline",
			finalizer: "management.astronomer.io/clusterbaseline-cleanup",
			specBits: []string{
				"clusterSelector:",
				"profileName:",
				"bundles:",
				"syncPolicy:",
				"applicationCount:",
			},
		},
		{
			file:      "crd-componentbundle.yaml",
			name:      "componentbundles.management.astronomer.io",
			kind:      "ComponentBundle",
			shortName: "astrobundle",
			finalizer: "management.astronomer.io/componentbundle-cleanup",
			specBits: []string{
				"defaultNamespace:",
				"source:",
				"secretRefs:",
				"healthChecks:",
				"capabilityRequirements:",
				"upgradePolicy:",
				"versions:",
				"availableVersions:",
			},
		},
		{
			file:      "crd-agentprofile.yaml",
			name:      "agentprofiles.management.astronomer.io",
			kind:      "AgentProfile",
			shortName: "astroagentprofile",
			finalizer: "management.astronomer.io/agentprofile-cleanup",
			specBits: []string{
				"privilegeProfile:",
				"namespace-viewer",
				"namespace-operator",
				"custom",
				"allowedRules:",
				"hostAccess:",
				"networkEgress:",
			},
		},
		{
			file:      "crd-gitopstarget.yaml",
			name:      "gitopstargets.management.astronomer.io",
			kind:      "GitOpsTarget",
			shortName: "astrogitopstarget",
			finalizer: "management.astronomer.io/gitopstarget-cleanup",
			specBits: []string{
				"selector:",
				"projectSelector:",
				"bundleRef:",
				"applicationSet:",
				"syncPolicy:",
				"syncWindows:",
				"applicationCount:",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			body := readChartTemplate(t, tt.file)
			for _, want := range []string{
				"{{- if .Values.crds.enabled }}",
				"kind: CustomResourceDefinition",
				"name: " + tt.name,
				"kind: " + tt.kind,
				"- " + tt.shortName,
				`"helm.sh/resource-policy": keep`,
				"management.astronomer.io/finalizer: " + tt.finalizer,
				"subresources:",
				"status: {}",
				"observedGeneration:",
				"conditions:",
				"lastTransitionTime:",
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

func TestManagementCRDRBACIncludesAllManagementKinds(t *testing.T) {
	body := readChartTemplate(t, "crd-rbac.yaml")
	for _, resource := range []string{
		"clusters",
		"projects",
		"clusterbaselines",
		"componentbundles",
		"agentprofiles",
		"gitopstargets",
		"clusterbaselines/status",
		"componentbundles/status",
		"agentprofiles/status",
		"gitopstargets/status",
		"clusterbaselines/finalizers",
		"componentbundles/finalizers",
		"agentprofiles/finalizers",
		"gitopstargets/finalizers",
		"applicationsets",
		"applications",
		"configmaps",
	} {
		if !strings.Contains(body, "- "+resource) {
			t.Fatalf("crd-rbac.yaml missing resource %q", resource)
		}
	}
}

func TestClusterCRDTemplateIncludesAgentProfileRef(t *testing.T) {
	body := readChartTemplate(t, "crd-cluster.yaml")
	for _, want := range []string{
		"profileRef:",
		"namespace-viewer",
		"namespace-operator",
		"custom",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("crd-cluster.yaml missing %q", want)
		}
	}
}

func TestCRDControllerServerEnvIncludesArgoNamespace(t *testing.T) {
	body := readChartTemplate(t, "server-deployment.yaml")
	for _, want := range []string{
		"CRD_ENABLED",
		"CRD_WATCH_NAMESPACE",
		"CRD_ARGO_NAMESPACE",
		".Values.crds.argoNamespace",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("server-deployment.yaml missing %q", want)
		}
	}
}
