package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"k8s.io/apimachinery/pkg/util/jsonmergepatch"
)

func renderDexRuntimeContract(t *testing.T) []renderedDoc {
	t.Helper()
	chart := filepath.Join(repoRoot(t), "deploy", "chart")
	cmd := exec.Command("helm", "template", "astronomer", chart,
		"--set", "dex.enabled=true",
		"--set", "dex.runtimeSecretName=dex-runtime-contract")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("render Dex runtime contract: %v\n%s", err, stderr.String())
	}
	return parseRenderedDocs(t, stdout.String())
}

func TestDexRuntimeSecretChartOwnsMetadataOnlyAndRBACIsExactName(t *testing.T) {
	docs := renderDexRuntimeContract(t)
	var secret, role renderedDoc
	for _, doc := range docs {
		metadata := nestedMap(doc, "metadata")
		name := stringValue(metadata["name"])
		switch stringValue(doc["kind"]) + "/" + name {
		case "Secret/dex-runtime-contract":
			secret = doc
		case "Role/astronomer-dex-runtime-writer":
			role = doc
		}
	}
	if secret == nil || role == nil {
		t.Fatalf("missing runtime Secret or writer Role")
	}
	if _, ok := secret["data"]; ok {
		t.Fatal("chart must not own Dex runtime Secret data")
	}
	if _, ok := secret["stringData"]; ok {
		t.Fatal("chart must not own Dex runtime Secret stringData")
	}
	secretJSON, _ := json.Marshal(secret)
	for _, forbidden := range []string{"sha256", "config-hash", "clientSecret", "bindPW"} {
		if strings.Contains(string(secretJSON), forbidden) {
			t.Fatalf("runtime Secret metadata contains forbidden content marker %q: %s", forbidden, secretJSON)
		}
	}
	rules, _ := role["rules"].([]any)
	var exactSecretRule, exactHealthProxyRule bool
	for _, raw := range rules {
		rule, _ := raw.(map[string]any)
		resources, _ := rule["resources"].([]any)
		verbs, _ := rule["verbs"].([]any)
		if containsAnyString(verbs, "create") {
			t.Fatalf("Dex writer Role must never grant Secret create: %#v", rule)
		}
		if containsAnyString(resources, "secrets") {
			names, _ := rule["resourceNames"].([]any)
			exactSecretRule = len(names) == 1 && names[0] == "dex-runtime-contract" &&
				containsAnyString(verbs, "get") && containsAnyString(verbs, "patch")
		}
		if containsAnyString(resources, "services/proxy") {
			names, _ := rule["resourceNames"].([]any)
			exactHealthProxyRule = len(names) == 1 && names[0] == "http:astronomer-dex:5556" && containsAnyString(verbs, "get")
		}
	}
	if !exactSecretRule {
		t.Fatalf("runtime Secret rule is not exact-name scoped: %#v", rules)
	}
	if !exactHealthProxyRule {
		t.Fatalf("Dex health proxy rule is not scoped to the full Kubernetes service-proxy resource name: %#v", rules)
	}
}

func TestDexRuntimeSecretThreeWayUpgradePreservesRuntimeOwnedData(t *testing.T) {
	var manifest renderedDoc
	for _, doc := range renderDexRuntimeContract(t) {
		if stringValue(doc["kind"]) == "Secret" && stringValue(nestedMap(doc, "metadata")["name"]) == "dex-runtime-contract" {
			manifest = doc
			break
		}
	}
	if manifest == nil {
		t.Fatal("runtime Secret manifest not found")
	}
	original, _ := json.Marshal(manifest)
	var modifiedDoc map[string]any
	_ = json.Unmarshal(original, &modifiedDoc)
	modifiedMeta := modifiedDoc["metadata"].(map[string]any)
	modifiedMeta["annotations"].(map[string]any)["astronomer.io/chart-upgrade-test"] = "v2"
	modified, _ := json.Marshal(modifiedDoc)
	var currentDoc map[string]any
	_ = json.Unmarshal(original, &currentDoc)
	currentDoc["data"] = map[string]any{"config.yaml": "cnVudGltZS1vd25lZC1zZWNyZXQ="}
	current, _ := json.Marshal(currentDoc)
	patch, err := jsonmergepatch.CreateThreeWayJSONMergePatch(original, modified, current)
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := jsonpatch.MergePatch(current, patch)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	_ = json.Unmarshal(upgraded, &result)
	data, _ := result["data"].(map[string]any)
	if data["config.yaml"] != "cnVudGltZS1vd25lZC1zZWNyZXQ=" {
		t.Fatalf("three-way chart upgrade removed runtime-owned data: patch=%s result=%s", patch, upgraded)
	}
	// Argo SSA has the same field-ownership prerequisite: data/stringData are
	// absent from every desired render, leaving them available to dex-handler.
	if bytes.Contains(modified, []byte(`"data"`)) || bytes.Contains(modified, []byte(`"stringData"`)) {
		t.Fatalf("upgraded desired manifest claimed runtime-owned fields: %s", modified)
	}
}

func TestDexRuntimeCutoverIsGatedOrderedAndZeroUnavailable(t *testing.T) {
	chart := filepath.Join(repoRoot(t), "deploy", "chart")
	cmd := exec.Command("helm", "template", "astronomer", chart,
		"--set", "dex.enabled=true",
		"--set", "dex.runtimeSecretName=dex-runtime-contract",
		"--set", "dex.migration.phase=cutover")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		t.Fatalf("render cutover: %v\n%s", err, stderr.String())
	}
	docs := parseRenderedDocs(t, stdout.String())
	var deployment, preflight, cleanup renderedDoc
	legacyFound := false
	for _, doc := range docs {
		name := stringValue(nestedMap(doc, "metadata")["name"])
		switch stringValue(doc["kind"]) + "/" + name {
		case "Deployment/astronomer-dex":
			deployment = doc
		case "ConfigMap/astronomer-dex-config":
			legacyFound = true
		case "Job/astronomer-preflight":
			preflight = doc
		case "Job/astronomer-dex-legacy-cleanup":
			cleanup = doc
		}
	}
	if deployment == nil || preflight == nil || cleanup == nil || legacyFound {
		t.Fatalf("invalid cutover resources: deployment=%v preflight=%v cleanup=%v legacy=%v", deployment != nil, preflight != nil, cleanup != nil, legacyFound)
	}
	rolling := nestedMap(deployment, "spec", "strategy", "rollingUpdate")
	if fmt.Sprint(rolling["maxUnavailable"]) != "0" || fmt.Sprint(rolling["maxSurge"]) != "1" {
		t.Fatalf("Dex cutover is not zero-unavailable: %#v", rolling)
	}
	volumes, _ := nestedMap(deployment, "spec", "template", "spec")["volumes"].([]any)
	volumeJSON, _ := json.Marshal(volumes)
	if !bytes.Contains(volumeJSON, []byte(`"secretName":"dex-runtime-contract"`)) || bytes.Contains(volumeJSON, []byte("configMap")) {
		t.Fatalf("Dex deployment did not cut over exclusively to runtime Secret: %s", volumeJSON)
	}
	preflightJSON, _ := json.Marshal(preflight)
	for _, required := range []string{"prepared Dex runtime Secret", "dexconfigcheck", "dex-migration-phase", "1 MiB preflight limit"} {
		if !bytes.Contains(preflightJSON, []byte(required)) {
			t.Fatalf("preflight missing cutover gate %q", required)
		}
	}
	cleanupJSON, _ := json.Marshal(cleanup)
	for _, required := range []string{"post-upgrade", "readyReplicas", "secretName", "kubectl delete configmap"} {
		if !bytes.Contains(cleanupJSON, []byte(required)) {
			t.Fatalf("cleanup missing post-rollout contract %q", required)
		}
	}
	cleanupText := string(cleanupJSON)
	if strings.Index(cleanupText, "readyReplicas") > strings.Index(cleanupText, "kubectl delete configmap") {
		t.Fatal("cleanup deletes the legacy ConfigMap before proving the Secret-mounted rollout ready")
	}
}

func TestDexFreshInstallNeverRendersLegacyConfigMap(t *testing.T) {
	for _, doc := range renderDexRuntimeContract(t) {
		if stringValue(doc["kind"]) == "ConfigMap" && stringValue(nestedMap(doc, "metadata")["name"]) == "astronomer-dex-config" {
			t.Fatal("fresh install rendered a legacy Dex ConfigMap")
		}
	}
}

func TestDexPrepareTemplatePreservesLiveDataAndOldMount(t *testing.T) {
	root := repoRoot(t)
	configTemplate, _ := os.ReadFile(filepath.Join(root, "deploy", "chart", "templates", "dex-legacy-prepare.yaml"))
	deploymentTemplate, _ := os.ReadFile(filepath.Join(root, "deploy", "chart", "templates", "dex-deployment.yaml"))
	for _, required := range []string{"pre-upgrade", "dex-config-retained", "--patch-file", "helm.sh/resource-policy", "astronomer.io/dex-migration-phase", "umask 077"} {
		if !bytes.Contains(configTemplate, []byte(required)) {
			t.Fatalf("prepare template missing %q", required)
		}
	}
	if bytes.Contains(configTemplate, []byte("lookup")) || bytes.Contains(configTemplate, []byte("$legacy.data")) || bytes.Contains(configTemplate, []byte("stringData:")) {
		t.Fatal("prepare manifest serializes credential data into Helm desired state")
	}
	if bytes.Contains(configTemplate, []byte(".Release.IsUpgrade")) {
		t.Fatal("prepare phase must not depend on imperative Helm upgrade state; Argo CD does not reliably provide it")
	}
	if !bytes.Contains(deploymentTemplate, []byte(`eq .Values.dex.migration.phase "prepare"`)) || !bytes.Contains(deploymentTemplate, []byte("configMap:")) {
		t.Fatal("prepare release does not keep the Deployment on the legacy ConfigMap")
	}
}

func TestDexRenderedReleaseNeverArchivesCredentialCanaries(t *testing.T) {
	chart := filepath.Join(repoRoot(t), "deploy", "chart")
	for _, phase := range []string{"fresh", "cutover"} {
		cmd := exec.Command("helm", "template", "astronomer", chart,
			"--set", "dex.enabled=true", "--set", "dex.migration.phase="+phase,
			"--set-string", "dex.runtimeSecretName=dex-runtime-contract")
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if err != nil {
			t.Fatalf("render %s: %v\n%s", phase, err, stderr.String())
		}
		for _, doc := range parseRenderedDocs(t, stdout.String()) {
			name := stringValue(nestedMap(doc, "metadata")["name"])
			if !strings.Contains(name, "dex") {
				continue
			}
			dexJSON, _ := json.Marshal(doc)
			for _, forbidden := range []string{"DEX-LEGACY-ARCHIVE-CANARY", "clientSecret", "bindPW", "stringData"} {
				if bytes.Contains(dexJSON, []byte(forbidden)) {
					t.Fatalf("%s Dex release resource contains forbidden credential shape %q", phase, forbidden)
				}
			}
		}
	}
	prepareTemplate, _ := os.ReadFile(filepath.Join(chart, "templates", "dex-legacy-prepare.yaml"))
	if bytes.Contains(prepareTemplate, []byte(".data.config.yaml")) || bytes.Contains(prepareTemplate, []byte("lookup")) {
		t.Fatal("prepare desired manifest reads credential bytes through Helm templating")
	}
}

func containsAnyString(items []any, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
