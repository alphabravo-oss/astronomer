package handler

import (
	"strings"
	"testing"
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
