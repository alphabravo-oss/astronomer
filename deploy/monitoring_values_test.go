package deploy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func loadYAMLMap(t *testing.T, rel string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := yaml.Unmarshal([]byte(readRepoFile(t, rel)), &out); err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	return out
}

// leafDiffs reports every leaf of overlay whose value differs from base at the
// same path. Lists are compared whole.
func leafDiffs(path string, overlay, base any) []string {
	om, ok := overlay.(map[string]any)
	if !ok {
		if !reflect.DeepEqual(overlay, base) {
			return []string{fmt.Sprintf("%s: overlay=%v live=%v", path, overlay, base)}
		}
		return nil
	}
	bm, _ := base.(map[string]any)
	var diffs []string
	for key, value := range om {
		diffs = append(diffs, leafDiffs(path+"."+key, value, bm[key])...)
	}
	return diffs
}

// TestMonitoringK3sOverlayMatchesLiveRelease proves values-k3s.yaml is exactly
// what the live astronomer-monitoring release (rev 11) runs with, so applying
// it changes nothing but packaging the hand-made alert-log objects.
func TestMonitoringK3sOverlayMatchesLiveRelease(t *testing.T) {
	overlay := loadYAMLMap(t, filepath.Join("deploy", "monitoring", "values-k3s.yaml"))
	live := loadYAMLMap(t, filepath.Join("deploy", "testdata", "live-values-astronomer-monitoring.yaml"))
	delete(overlay, "extraManifests")
	if diffs := leafDiffs("", overlay, live); len(diffs) > 0 {
		slices.Sort(diffs)
		t.Fatalf("values-k3s.yaml drifted from the live release:\n%s", strings.Join(diffs, "\n"))
	}
}

func TestMonitoringK3sOverlayContract(t *testing.T) {
	raw := readRepoFile(t, filepath.Join("deploy", "monitoring", "values-k3s.yaml"))
	values := loadYAMLMap(t, filepath.Join("deploy", "monitoring", "values-k3s.yaml"))
	for _, component := range []string{"kubeControllerManager", "kubeScheduler", "kubeProxy"} {
		if nestedMap(values, component)["enabled"] != false {
			t.Errorf("%s scraping must be disabled on k3s", component)
		}
	}
	if stringAt(values, "prometheus", "prometheusSpec", "retentionSize") == "" {
		t.Error("prometheusSpec.retentionSize unset")
	}
	config := nestedMap(values, "alertmanager", "config")
	if stringAt(config, "route", "receiver") != "alert-log" {
		t.Errorf("default route receiver = %q, want alert-log", stringAt(config, "route", "receiver"))
	}
	secrets, _ := nestedMap(values, "alertmanager", "alertmanagerSpec")["secrets"].([]any)
	if !slices.Contains(secrets, any("alertmanager-slack")) {
		t.Fatalf("alertmanagerSpec.secrets = %v", secrets)
	}
	if regexp.MustCompile(`(?m)^\s*(api_url|url):\s*['"]?https://hooks\.slack\.com`).MatchString(raw) || strings.Contains(raw, "api_url:") {
		t.Error("Slack webhook must come from api_url_file, never inline")
	}
	var slackFile, webhookURL string
	for _, receiver := range config["receivers"].([]any) {
		r := receiver.(map[string]any)
		if cfgs, ok := r["slack_configs"].([]any); ok {
			slackFile = stringValue(cfgs[0].(map[string]any)["api_url_file"])
		}
		if cfgs, ok := r["webhook_configs"].([]any); ok && r["name"] == "alert-log" {
			webhookURL = stringValue(cfgs[0].(map[string]any)["url"])
		}
	}
	if slackFile != "/etc/alertmanager/secrets/alertmanager-slack/url" {
		t.Errorf("slack api_url_file = %q", slackFile)
	}
	if webhookURL != "http://alert-log.astronomer-monitoring.svc:8080/alert" {
		t.Errorf("alert-log webhook = %q", webhookURL)
	}
	kinds := map[string]bool{}
	for _, manifest := range values["extraManifests"].([]any) {
		m := manifest.(map[string]any)
		kinds[stringValue(m["kind"])+"/"+stringAt(m, "metadata", "name")] = true
		if m["kind"] != "Deployment" {
			continue
		}
		for _, container := range containerList(nestedMap(m, "spec", "template", "spec"), "containers") {
			if !regexp.MustCompile(`^[^@\s]+:[^@\s]+@sha256:[a-f0-9]{64}$`).MatchString(stringValue(container["image"])) {
				t.Errorf("alert-log image %q is not tag@digest", container["image"])
			}
		}
	}
	for _, want := range []string{"Deployment/alert-log", "Service/alert-log", "ConfigMap/alert-log-response", "Secret/alertmanager-slack"} {
		if !kinds[want] {
			t.Errorf("extraManifests lacks %s", want)
		}
	}
}

// TestMonitoringK3sOverlayRendersWithKubePrometheusStack renders the upstream
// chart with the live values plus the repo overlays when KPS_CHART points at a
// kube-prometheus-stack 61.x chart directory or archive (not vendored).
func TestMonitoringK3sOverlayRendersWithKubePrometheusStack(t *testing.T) {
	chart := os.Getenv("KPS_CHART")
	if chart == "" {
		t.Skip("KPS_CHART not set; skipping upstream kube-prometheus-stack render")
	}
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	root := repoRoot(t)
	cmd := exec.Command("helm", "template", "astronomer-monitoring", chart, "--namespace", "astronomer-monitoring", "--kube-version", k3sKubeVersion,
		"-f", filepath.Join(root, "deploy", "testdata", "live-values-astronomer-monitoring.yaml"),
		"-f", filepath.Join(root, "deploy", "monitoring", "values-single-node.yaml"),
		"-f", filepath.Join(root, "deploy", "monitoring", "values-k3s.yaml"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helm template kube-prometheus-stack: %v", err)
	}
	docs := parseRenderedDocs(t, string(out))
	var promSpec, amSpec map[string]any
	for _, doc := range docs {
		switch doc["kind"] {
		case "Prometheus":
			promSpec = nestedMap(doc, "spec")
		case "Alertmanager":
			amSpec = nestedMap(doc, "spec")
		case "Service", "ServiceMonitor":
			name := stringAt(doc, "metadata", "name")
			for _, gone := range []string{"kube-controller-manager", "kube-scheduler", "kube-proxy"} {
				if strings.HasSuffix(name, gone) {
					t.Errorf("k3s render still creates %s %s", doc["kind"], name)
				}
			}
		}
	}
	if promSpec["retentionSize"] != "15GB" || promSpec["retention"] != "15d" {
		t.Errorf("Prometheus retention = %v / %v", promSpec["retention"], promSpec["retentionSize"])
	}
	if secrets, _ := amSpec["secrets"].([]any); !slices.Contains(secrets, any("alertmanager-slack")) {
		t.Errorf("Alertmanager secrets = %v", amSpec["secrets"])
	}
	seed := findRenderedDoc(t, docs, "Secret", "alertmanager-slack")
	if stringAt(seed, "stringData", "url") != "http://alert-log.astronomer-monitoring.svc:8081/slack-placeholder" {
		t.Errorf("alertmanager-slack seed = %v", seed["stringData"])
	}
	findRenderedDoc(t, docs, "Deployment", "alert-log")
	findRenderedDoc(t, docs, "Service", "alert-log")
}
