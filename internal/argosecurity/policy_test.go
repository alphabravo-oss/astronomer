package argosecurity

import (
	"encoding/json"
	"strings"
	"testing"
)

const policyCanary = "ARGO_POLICY_CANARY_7f41d8"

func TestSanitizeRedactsEveryArgoSourceCopyAndPreservesDiagnostics(t *testing.T) {
	source := map[string]any{
		"repoURL": "https://git.example/repo",
		"helm": map[string]any{
			"values":         "password: " + policyCanary,
			"valuesObject":   map[string]any{"innocent": policyCanary},
			"parameters":     []any{map[string]any{"name": "x", "value": policyCanary}},
			"fileParameters": []any{map[string]any{"name": "x", "path": policyCanary}},
			"valueFiles":     []any{policyCanary},
			"releaseName":    "safe-release",
		},
	}
	payload := map[string]any{
		"metadata":  map[string]any{"name": "example"},
		"spec":      map[string]any{"source": source},
		"operation": map[string]any{"sync": map[string]any{"source": source}},
		"status": map[string]any{
			"health":         map[string]any{"status": "Healthy", "message": "diagnostic retained"},
			"sync":           map[string]any{"comparedTo": map[string]any{"source": source}},
			"history":        []any{map[string]any{"source": source}},
			"operationState": map[string]any{"syncResult": map[string]any{"source": source}},
		},
	}
	raw, err := json.Marshal(Sanitize(payload))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, policyCanary) {
		t.Fatalf("source canary survived: %s", text)
	}
	for _, diagnostic := range []string{"Healthy", "diagnostic retained", "safe-release", "https://git.example/repo"} {
		if !strings.Contains(text, diagnostic) {
			t.Fatalf("diagnostic %q was removed: %s", diagnostic, text)
		}
	}
}

func TestSanitizeRedactsSecretAndLegacyConfigMapManifests(t *testing.T) {
	payload := map[string]any{"manifests": []any{
		"apiVersion: v1\nkind: Secret\nmetadata:\n  name: db\ndata:\n  password: " + policyCanary + "\nstringData:\n  token: " + policyCanary + "\n",
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: legacy\ndata:\n  config.yaml: |\n    connector:\n      clientSecret: " + policyCanary + "\n  safe: diagnostic\n",
	}}
	raw, _ := json.Marshal(Sanitize(payload))
	text := string(raw)
	if strings.Contains(text, policyCanary) {
		t.Fatalf("manifest canary survived: %s", text)
	}
	for _, want := range []string{"Secret", "ConfigMap", "db", "legacy", "diagnostic"} {
		if !strings.Contains(text, want) {
			t.Fatalf("manifest diagnostic %q missing: %s", want, text)
		}
	}
}

func TestSanitizeURLCredentialsPreservesSafeDiagnostics(t *testing.T) {
	payload := map[string]any{
		"repoURL":  "https://user:" + policyCanary + "@git.example/team/repo?token=" + policyCanary,
		"chartURL": "https://charts.example/platform/index.yaml?client_secret=" + policyCanary,
	}
	raw, _ := json.Marshal(Sanitize(payload))
	text := string(raw)
	if strings.Contains(text, policyCanary) || strings.Contains(text, "user:") {
		t.Fatalf("URL credentials survived: %s", text)
	}
	for _, want := range []string{"https://", "git.example", "/team/repo", "charts.example", "/platform/index.yaml"} {
		if !strings.Contains(text, want) {
			t.Fatalf("URL diagnostic %q missing: %s", want, text)
		}
	}
}

func TestValidateMutationRejectsEverySourceBypass(t *testing.T) {
	cases := map[string]any{
		"helm values":          map[string]any{"spec": map[string]any{"source": map[string]any{"helm": map[string]any{"values": policyCanary}}}},
		"values object":        map[string]any{"spec": map[string]any{"source": map[string]any{"helm": map[string]any{"valuesObject": map[string]any{"safe": policyCanary}}}}},
		"parameters":           map[string]any{"spec": map[string]any{"source": map[string]any{"helm": map[string]any{"parameters": []any{map[string]any{"name": "x", "value": policyCanary}}}}}},
		"file parameters":      map[string]any{"spec": map[string]any{"source": map[string]any{"helm": map[string]any{"fileParameters": []any{map[string]any{"name": "x", "path": policyCanary}}}}}},
		"value files":          map[string]any{"spec": map[string]any{"source": map[string]any{"helm": map[string]any{"valueFiles": []any{"secret.yaml"}}}}},
		"multi source":         map[string]any{"spec": map[string]any{"sources": []any{map[string]any{"repoURL": "https://git.example/repo"}}}},
		"plugin env":           map[string]any{"spec": map[string]any{"source": map[string]any{"plugin": map[string]any{"env": []any{map[string]any{"name": "TOKEN", "value": policyCanary}}}}}},
		"operation source":     map[string]any{"operation": map[string]any{"sync": map[string]any{"source": map[string]any{"repoURL": "https://git.example/repo"}}}},
		"local manifests":      map[string]any{"operation": map[string]any{"sync": map[string]any{"manifests": []any{policyCanary}}}},
		"freeform info":        map[string]any{"info": []any{map[string]any{"name": "note", "value": policyCanary}}},
		"secret key":           map[string]any{"spec": map[string]any{"source": map[string]any{"clientSecret": policyCanary}}},
		"credential URL":       map[string]any{"spec": map[string]any{"source": map[string]any{"repoURL": "https://user:" + policyCanary + "@git.example/repo"}}},
		"patch string":         map[string]any{"patch": `{"spec":{"source":{"helm":{"values":"` + policyCanary + `"}}}}`},
		"json patch path":      []any{map[string]any{"op": "replace", "path": "/spec/source/helm/values", "value": policyCanary}},
		"json patch copy from": []any{map[string]any{"op": "copy", "from": "/spec/source/helm/values", "path": "/metadata/name"}},
		"unknown source field": map[string]any{"spec": map[string]any{"source": map[string]any{"repoURL": "https://git.example/repo", "futureCredentialBlob": policyCanary}}},
		"helm future field":    map[string]any{"spec": map[string]any{"source": map[string]any{"helm": map[string]any{"futureInline": policyCanary}}}},
		"metadata annotation":  map[string]any{"metadata": map[string]any{"annotations": map[string]any{"example.com/note": policyCanary}}},
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateMutation(payload); err == nil {
				t.Fatal("unsafe mutation accepted")
			}
		})
	}
}

func TestValidateMutationAllowsReferenceOnlySourceAndAction(t *testing.T) {
	for name, payload := range map[string]any{
		"source": map[string]any{"spec": map[string]any{"source": map[string]any{
			"repoURL": "https://git.example/repo", "path": "deploy", "targetRevision": "main",
			"helm": map[string]any{"releaseName": "example"},
		}}},
		"sync":            map[string]any{"name": "example", "revision": "main", "prune": false},
		"safe json patch": []any{map[string]any{"op": "replace", "path": "/spec/source/targetRevision", "value": "stable"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateMutation(payload); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateMutationJSONRejectsScalarWrapper(t *testing.T) {
	raw, _ := json.Marshal(`{"spec":{"source":{"helm":{"values":"` + policyCanary + `"}}}}`)
	if err := ValidateMutationJSON(raw); err == nil {
		t.Fatal("JSON string wrapper was accepted")
	}
}
