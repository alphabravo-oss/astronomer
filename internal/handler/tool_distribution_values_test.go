package handler

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDistributionFamily(t *testing.T) {
	cases := map[string]string{
		"k3s":            "k3s",
		"k3s-v1.30":      "k3s",
		"k3d":            "k3s",
		"rke2":           "rke2",
		"OpenShift 4.15": "openshift",
		"okd":            "openshift",
		"eks":            "eks",
		"aks":            "aks",
		"gke":            "gke",
		"kubeadm":        "vanilla",
		"":               "",
	}
	for in, want := range cases {
		if got := distributionFamily(in); got != want {
			t.Errorf("distributionFamily(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDistributionInstallValues(t *testing.T) {
	// fluent-bit on k3s drops the machine-id mount and points at CRI pod logs.
	v := distributionInstallValues("fluent-bit", "k3s")
	if !strings.Contains(v, "daemonSetVolumes") || strings.Contains(v, "machine-id") {
		t.Errorf("k3s fluent-bit override should set daemonSetVolumes without machine-id:\n%s", v)
	}
	if !strings.Contains(v, "/var/log/pods") {
		t.Errorf("k3s fluent-bit override should mount CRI pod logs:\n%s", v)
	}
	// OpenShift gets the privileged SCC instead.
	if oc := distributionInstallValues("fluent-bit", "OpenShift 4.15"); !strings.Contains(oc, "privileged: true") {
		t.Errorf("openshift fluent-bit override should be privileged:\n%s", oc)
	}
	// Vanilla / unknown distributions get no override (chart defaults stand).
	if got := distributionInstallValues("fluent-bit", "kubeadm"); got != "" {
		t.Errorf("vanilla should yield no override, got:\n%s", got)
	}
	// A tool with no distribution quirks yields nothing.
	if got := distributionInstallValues("trivy-operator", "k3s"); got != "" {
		t.Errorf("tool without overrides should yield nothing, got:\n%s", got)
	}
}

func TestCatalogValuesMergeDistributionDefaultsBeforeOperatorValues(t *testing.T) {
	values := mergeValueLayers(
		distributionInstallValues("fluent-bit", "K3s"),
		"config:\n  outputs: |\n    [OUTPUT]\n        Name stdout\n        Match *\n",
	)
	if !strings.Contains(values, "path: /var/log/pods") || !strings.Contains(values, "Name stdout") {
		t.Fatalf("catalog values should preserve the K3s volume adaptation and operator output:\n%s", values)
	}
	if strings.Contains(values, "machine-id") {
		t.Fatalf("catalog K3s values must not restore the unsupported machine-id mount:\n%s", values)
	}

	overridden := mergeValueLayers(
		distributionInstallValues("fluent-bit", "K3s"),
		"daemonSetVolumes:\n  - name: custom\n    emptyDir: {}\n",
	)
	var decoded map[string]any
	if err := yaml.Unmarshal([]byte(overridden), &decoded); err != nil {
		t.Fatalf("decode merged catalog values: %v", err)
	}
	volumes, _ := decoded["daemonSetVolumes"].([]any)
	volume, _ := volumes[0].(map[string]any)
	if len(volumes) != 1 || volume["name"] != "custom" {
		t.Fatalf("operator catalog values should override a distribution-provided list:\n%s", overridden)
	}
}

func TestConstellationCatalogDefaultsExemptSupportedSystemNamespaces(t *testing.T) {
	values := catalogInstallValues("constellation", "K3s", "image:\n  tag: v0.2.0\n")
	var decoded map[string]any
	if err := yaml.Unmarshal([]byte(values), &decoded); err != nil {
		t.Fatalf("decode Constellation integration values: %v", err)
	}
	for _, namespace := range []string{"cis-operator-system", "longhorn-system", "cattle-neuvector-system", "istio-system", "astronomer-monitoring", "astronomer-gatekeeper-system"} {
		if !strings.Contains(values, "- "+namespace+"\n") {
			t.Errorf("Constellation integration values do not exempt supported namespace %q:\n%s", namespace, values)
		}
	}
	image, _ := decoded["image"].(map[string]any)
	if image["tag"] != "v0.2.0" {
		t.Fatalf("operator values were not preserved: %+v", decoded)
	}
}

func TestConstellationOperatorCanReplaceSystemNamespaceExemptions(t *testing.T) {
	values := catalogInstallValues("constellation", "K3s", `admission:
  webhook:
    namespaceSelector:
      matchExpressions:
        - key: team
          operator: In
          values: [platform]
`)
	if strings.Contains(values, "cis-operator-system") || !strings.Contains(values, "key: team") {
		t.Fatalf("operator namespace selector did not replace the platform default:\n%s", values)
	}
}
