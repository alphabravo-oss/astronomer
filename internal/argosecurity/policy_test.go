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
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateMutation(payload); err == nil {
				t.Fatal("wrapped unsafe mutation accepted")
			}
		})
	}
	if err := ValidateMutation(map[string]any{"description": "[ordinary bracket text", "note": unsafe}); err != nil {
		t.Fatalf("ordinary strings were interpreted as encoded mutations: %v", err)
	}
}

func TestSanitizeMalformedAndOversizedWrappersFailClosed(t *testing.T) {
	for name, value := range map[string]string{
		"malformed object": `{"token":"` + policyCanary,
		"malformed array":  `[ordinary bracket text`,
		"oversized":        `{"safe":"` + strings.Repeat("x", maxStructuredStringLen) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			got := Sanitize(map[string]any{"message": value}).(map[string]any)["message"]
			if got != Marker {
				t.Fatalf("wrapper sanitized to %#v, want marker", got)
			}
		})
	}
}

func TestSanitizePrefixedCredentialURLsAndCloudSignatures(t *testing.T) {
	input := "upstream failed at https://user:pass@example.test/object?X-Amz-Signature=" + policyCanary + "#fragment; retry Signature=" + policyCanary
	got := SanitizeString(input)
	if strings.Contains(got, policyCanary) || strings.Contains(got, "user:pass") || strings.Contains(got, "?") || strings.Contains(got, "#fragment") {
		t.Fatalf("prefixed diagnostic leaked credential: %q", got)
	}
	if !strings.Contains(got, "upstream failed at https://example.test/object") || !strings.Contains(got, Marker) {
		t.Fatalf("safe diagnostic context lost: %q", got)
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

func TestRepositoryURLPolicyAllowsConventionalGitIdentityOnly(t *testing.T) {
	for _, safe := range []string{
		"git@github.com:team/repo.git",
		"ssh://git@git.example/team/repo.git",
		"git://git@git.example/team/repo.git",
		"https://git.example/team/repo.git",
	} {
		if err := ValidateRepositoryURL(safe); err != nil {
			t.Errorf("safe repository URL %q rejected: %v", safe, err)
		}
	}
	for _, unsafe := range []string{
		"root@git.example:team/repo.git",
		"git@git.example:../repo.git",
		"git@git.example:/etc/repo.git",
		"ssh://git:password@git.example/team/repo.git",
		"ssh://root@git.example/team/repo.git",
		"https://git@git.example/team/repo.git",
		"https://git.example/team/repo.git?token=secret",
	} {
		if err := ValidateRepositoryURL(unsafe); err == nil {
			t.Errorf("unsafe repository URL %q accepted", unsafe)
		}
	}
	if err := ValidateCredentialFreeURL("ssh://git@git.example/team/repo.git"); err == nil {
		t.Fatal("strict endpoint validator accepted userinfo")
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

func TestValidateApplicationSetGeneratorRejectsCredentialScalars(t *testing.T) {
	for _, scalar := range []string{
		"Bearer " + policyCanary,
		"token=" + policyCanary,
		"credential=" + policyCanary,
		"fetch https://storage.example/object?X-Amz-Signature=" + policyCanary,
	} {
		payload := map[string]any{"spec": map[string]any{"generators": []any{map[string]any{"list": map[string]any{"elements": []any{map[string]any{"note": scalar}}}}}}}
		if err := ValidateMutation(payload); err == nil {
			t.Errorf("credential generator scalar %q accepted", scalar)
		}
	}
	if err := ValidateMutation(map[string]any{"spec": map[string]any{"generators": []any{map[string]any{"list": map[string]any{"elements": []any{map[string]any{"credential": "opaque"}}}}}}}); err == nil {
		t.Fatal("singular credential key accepted")
	}
}

func TestValidateMutationProjectDestinationWildcardIsSchemaAndPathScoped(t *testing.T) {
	project := []byte(`{"spec":{"description":"[ordinary]","destinations":[{"server":"*","namespace":"*"}]}}`)
	if err := ValidateMutationJSONForPath(project, "/argocd/api/v1/projects/demo"); err != nil {
		t.Fatalf("project destination wildcard rejected: %v", err)
	}
	for _, tc := range []struct {
		path string
		raw  string
	}{
		{"/argocd/api/v1/applications/demo", `{"spec":{"destination":{"server":"*"}}}`},
		{"/argocd/api/v1/applications/demo", `{"spec":{"destinations":[{"server":"*"}]}}`},
		{"/argocd/api/v1/projects/demo", `{"spec":{"server":"*"}}`},
	} {
		if err := ValidateMutationJSONForPath([]byte(tc.raw), tc.path); err == nil {
			t.Errorf("wildcard smuggling accepted for %s: %s", tc.path, tc.raw)
		}
	}
}

func TestValidateMutationAllowsConstrainedMultiSourceHelmValueRepositories(t *testing.T) {
	safe := map[string]any{"spec": map[string]any{"sources": []any{
		map[string]any{"repoURL": "https://charts.example/repo", "chart": "platform", "helm": map[string]any{"valueFiles": []any{"$values/environments/prod.yaml", "defaults/base.yaml"}}},
		map[string]any{"repoURL": "git@github.com:team/values.git", "targetRevision": "main", "ref": "values"},
	}}}
	if err := ValidateMutation(safe); err != nil {
		t.Fatalf("safe value repository rejected: %v", err)
	}
	for name, valueFile := range map[string]string{
		"traversal":   "$values/../secret.yaml",
		"absolute":    "/etc/secret",
		"url":         "https://evil.example/values.yaml",
		"template":    "{{values}}/prod.yaml",
		"credential":  "token=secret",
		"unknown ref": "$unknown/prod.yaml",
		"query":       "$values/prod.yaml?token=secret",
	} {
		t.Run(name, func(t *testing.T) {
			payload := map[string]any{"spec": map[string]any{"sources": []any{
				map[string]any{"repoURL": "https://charts.example/repo", "chart": "platform", "helm": map[string]any{"valueFiles": []any{valueFile}}},
				map[string]any{"repoURL": "https://git.example/values", "ref": "values"},
			}}}
			if err := ValidateMutation(payload); err == nil {
				t.Fatal("unsafe value repository reference accepted")
			}
		})
	}
	if err := ValidateMutation(map[string]any{"spec": map[string]any{"source": map[string]any{"repoURL": "https://charts.example/repo", "helm": map[string]any{"valueFiles": []any{"defaults.yaml"}}}}}); err == nil {
		t.Fatal("single-source valueFiles accepted")
	}
}

func TestValidateMutationJSONRejectsScalarWrapper(t *testing.T) {
	raw, _ := json.Marshal(`{"spec":{"source":{"helm":{"values":"` + policyCanary + `"}}}}`)
	if err := ValidateMutationJSON(raw); err == nil {
		t.Fatal("JSON string wrapper was accepted")
	}
}
