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
		"multi source values":  map[string]any{"spec": map[string]any{"sources": []any{map[string]any{"repoURL": "https://git.example/repo", "helm": map[string]any{"values": policyCanary}}}}},
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
		"multi source": map[string]any{"spec": map[string]any{"sources": []any{
			map[string]any{"repoURL": "https://git.example/repo", "path": "deploy"},
			map[string]any{"repoURL": "https://charts.example/repo", "chart": "platform"},
		}}},
		"applicationset safe metadata": map[string]any{
			"metadata": map[string]any{"annotations": map[string]any{
				"argocd.argoproj.io/sync-wave":                             "-1",
				"argocd.argoproj.io/compare-options":                       "IgnoreExtraneous",
				"notifications.argoproj.io/subscribe.on-sync-failed.slack": "platform-alerts",
			}},
			"spec": map[string]any{"generators": []any{map[string]any{"list": map[string]any{
				"elements": []any{map[string]any{"cluster": "prod", "url": "https://kube.example"}},
			}}}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateMutation(payload); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSanitizeRecursesThroughCompleteJSONWrappersAndPatches(t *testing.T) {
	inner := `[{"op":"replace","path":"/status","value":{"token":"` + policyCanary + `"}}]`
	double, err := json.Marshal(inner)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"response": string(double), "array": []any{inner}}
	raw, _ := json.Marshal(Sanitize(payload))
	if strings.Contains(string(raw), policyCanary) {
		t.Fatalf("wrapped JSON leaked canary: %s", raw)
	}
}

func TestValidateMutationRejectsNestedJSONWrapperSmuggling(t *testing.T) {
	unsafe := `{"spec":{"source":{"helm":{"values":"` + policyCanary + `"}}}}`
	double, _ := json.Marshal(unsafe)
	for name, payload := range map[string]any{
		"double": map[string]any{"patch": string(double)},
		"array":  map[string]any{"items": []any{unsafe}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateMutation(payload); err == nil {
				t.Fatal("wrapped unsafe mutation accepted")
			}
		})
	}
}

func TestCredentialFreeURLPolicy(t *testing.T) {
	for _, safe := range []string{"https://git.example/team/repo", "ssh://git.example/team/repo", "{{server}}"} {
		if err := ValidateCredentialFreeURL(safe); err != nil {
			t.Errorf("safe URL %q rejected: %v", safe, err)
		}
	}
	for _, unsafe := range []string{
		"https://user:pass@git.example/repo",
		"https://git.example/repo?ref=main",
		"https://s3.example/object?X-Amz-Signature=secret",
		"https://storage.googleapis.com/object?GoogleAccessId=x&Signature=secret",
		"https://blob.core.windows.net/c/o?sv=1&sig=secret",
		"https://git.example/repo#revision",
	} {
		if err := ValidateCredentialFreeURL(unsafe); err == nil {
			t.Errorf("unsafe URL %q accepted", unsafe)
		}
	}
	if err := ValidateSourceRepoPattern("!https://git.example/private/*"); err != nil {
		t.Fatalf("safe negated wildcard rejected: %v", err)
	}
}

func TestSanitizeURLDiagnosticsStripAllQueryFragmentAndUserinfo(t *testing.T) {
	got := SanitizeString("https://user:pass@git.example/repo?ref=main#revision")
	if got != "https://git.example/repo" {
		t.Fatalf("sanitized URL = %q", got)
	}
}

func TestValidateApplicationSetGeneratorAndAnnotationBounds(t *testing.T) {
	unsafe := []any{
		map[string]any{"spec": map[string]any{"generators": []any{map[string]any{"list": map[string]any{"elements": []any{map[string]any{"password": "x"}}}}}}},
		map[string]any{"spec": map[string]any{"generators": []any{map[string]any{"list": map[string]any{"elements": []any{map[string]any{"nested": map[string]any{"x": "y"}}}}}}}},
		map[string]any{"metadata": map[string]any{"annotations": map[string]any{"example.com/future": "value"}}},
		map[string]any{"metadata": map[string]any{"annotations": map[string]any{"argocd.argoproj.io/sync-wave": "1000"}}},
	}
	for i, payload := range unsafe {
		if err := ValidateMutation(payload); err == nil {
			t.Errorf("unsafe ApplicationSet payload %d accepted", i)
		}
	}
}

func TestValidateMutationJSONRejectsScalarWrapper(t *testing.T) {
	raw, _ := json.Marshal(`{"spec":{"source":{"helm":{"values":"` + policyCanary + `"}}}}`)
	if err := ValidateMutationJSON(raw); err == nil {
		t.Fatal("JSON string wrapper was accepted")
	}
}
