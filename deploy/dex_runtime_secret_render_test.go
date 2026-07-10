package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	var exactSecretRule bool
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
	}
	if !exactSecretRule {
		t.Fatalf("runtime Secret rule is not exact-name scoped: %#v", rules)
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
	docs := renderDexRuntimeContract(t)
	var deployment, legacyConfig, preflight renderedDoc
	for _, doc := range docs {
		name := stringValue(nestedMap(doc, "metadata")["name"])
		switch stringValue(doc["kind"]) + "/" + name {
		case "Deployment/astronomer-dex":
			deployment = doc
		case "ConfigMap/astronomer-dex-config":
			legacyConfig = doc
		case "Job/astronomer-preflight":
			preflight = doc
		}
	}
	if deployment == nil || legacyConfig == nil || preflight == nil {
		t.Fatalf("missing cutover resources: deployment=%v config=%v preflight=%v", deployment != nil, legacyConfig != nil, preflight != nil)
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
	legacyJSON, _ := json.Marshal(legacyConfig)
	if bytes.Contains(legacyJSON, []byte("config.yaml")) || !bytes.Contains(legacyJSON, []byte("migration-notice")) {
		t.Fatalf("legacy ConfigMap was not inert after cutover: %s", legacyJSON)
	}
	if stringValue(nestedMap(legacyConfig, "metadata", "annotations")["argocd.argoproj.io/sync-wave"]) != "1" ||
		stringValue(nestedMap(deployment, "metadata", "annotations")["argocd.argoproj.io/sync-wave"]) != "0" {
		t.Fatalf("Argo cutover waves do not scrub after Deployment convergence")
	}
	preflightJSON, _ := json.Marshal(preflight)
	for _, required := range []string{"prepared Dex runtime Secret", "has no config.yaml data", "dex-runtime-secret-migration.md"} {
		if !bytes.Contains(preflightJSON, []byte(required)) {
			t.Fatalf("preflight missing cutover gate %q", required)
		}
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
