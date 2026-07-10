package argosecurity

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
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

func TestSanitizeArbitraryBracketTextRoundTripsWithoutCredentialLeak(t *testing.T) {
	for _, ordinary := range []string{`[ordinary bracket text`, `"quoted prose that is not JSON`} {
		if got := Sanitize(map[string]any{"description": ordinary}).(map[string]any)["description"]; got != ordinary {
			t.Fatalf("ordinary bracket/quote text = %#v", got)
		}
	}
	malformed := `{"token":"` + policyCanary
	got := Sanitize(map[string]any{"message": malformed}).(map[string]any)["message"].(string)
	if strings.Contains(got, policyCanary) || !strings.Contains(got, Marker) {
		t.Fatalf("malformed diagnostic was not safely preserved: %q", got)
	}
	oversized := `[ordinary ` + strings.Repeat("x", maxStructuredStringLen) + ` text`
	if got := Sanitize(map[string]any{"description": oversized}).(map[string]any)["description"]; got != oversized {
		t.Fatal("oversized ordinary bracket text did not round-trip")
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

func TestSanitizeEmbeddedHTTPURLsCaseInsensitively(t *testing.T) {
	got := SanitizeString("clone HTTP://user:pass@example.test/repo?AWSAccessKeyId=" + policyCanary + "#fragment then continue")
	for _, forbidden := range []string{policyCanary, "user:pass", "AWSAccessKeyId", "#fragment"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("case-insensitive URL sanitizer leaked %q: %s", forbidden, got)
		}
	}
	if !strings.Contains(strings.ToLower(got), "http://example.test/repo") || !strings.Contains(got, "then continue") {
		t.Fatalf("URL sanitizer lost diagnostics: %s", got)
	}
}

func TestSanitizeDurableReasonStructuredAndProse(t *testing.T) {
	ordinary := `[maintenance "quoted" note`
	got, err := SanitizeDurableReason(ordinary)
	if err != nil || got != ordinary {
		t.Fatalf("ordinary reason = %q, %v", got, err)
	}
	structured := `{"apiKey":"` + policyCanary + `","nested":{"privateKey":"` + policyCanary + `","credential":"` + policyCanary + `"},"note":"Bearer ` + policyCanary + `"}`
	got, err = SanitizeDurableReason(structured)
	if err != nil || strings.Contains(got, policyCanary) || !strings.Contains(got, Marker) {
		t.Fatalf("structured reason = %q, %v", got, err)
	}
	plain := "apiKey=" + policyCanary + " privateKey=" + policyCanary + " credential=" + policyCanary + " token=" + policyCanary + " Bearer " + policyCanary
	got, err = SanitizeDurableReason(plain)
	if err != nil || strings.Contains(got, policyCanary) {
		t.Fatalf("plain reason = %q, %v", got, err)
	}
}

func TestCanonicalCredentialDetectorTaxonomyAndFragmentedKeys(t *testing.T) {
	keys := []string{
		"password", "passwd", "secret", "clientSecret", "client_cert_key", "kube-config",
		"apiKey", "private.key", "token", "bearer", "credential", "AWSAccessKeyId", "sig",
	}
	for _, key := range keys {
		for _, input := range []string{
			key + "=" + policyCanary,
			`"` + key + `":"` + policyCanary + `"`,
			key + ": " + policyCanary,
		} {
			got := SanitizeString(input)
			if strings.Contains(got, policyCanary) || !strings.Contains(got, Marker) {
				t.Errorf("key %q input %q sanitized to %q", key, input, got)
			}
		}
	}
	for _, fragmented := range []string{
		`{"client_"secret":"` + policyCanary,
		`{"pass-word":"` + policyCanary,
		`[broken "private_ key" = ` + policyCanary,
	} {
		got := Sanitize(map[string]any{"message": fragmented}).(map[string]any)["message"]
		if got != Marker {
			t.Errorf("fragmented malformed key was not failed closed: %#v", got)
		}
	}
}

func TestCanonicalCredentialSeparatorFamiliesSanitizeAndRejectEveryGeneratorBranch(t *testing.T) {
	families := append(append([]string(nil), canonicalSensitiveFragments...), "sig")
	fragment := func(key string) string {
		separators := []string{"·", "/", "_", `"`, "-", "."}
		var out strings.Builder
		for i, r := range key {
			if i > 0 {
				out.WriteString(separators[(i-1)%len(separators)])
			}
			out.WriteRune(r)
		}
		return out.String()
	}
	wrap := func(generator any) map[string]any {
		return map[string]any{"spec": map[string]any{"generators": []any{generator}}}
	}
	listWith := func(value string) any {
		return map[string]any{"list": map[string]any{"elements": []any{map[string]any{"note": value}}}}
	}
	gitWith := func(value string) any {
		return map[string]any{"git": map[string]any{"repoURL": "https://git.example/repo", "values": map[string]any{"note": value}}}
	}
	clusterWith := func(value string) any {
		return map[string]any{"clusters": map[string]any{"values": map[string]any{"note": value}}}
	}
	assignments := make(map[string]string, len(families)+6)
	for _, family := range families {
		assignments["fragmented "+family] = fragment(family) + "=" + policyCanary
	}
	for _, family := range []string{"api key", "private key", "client cert key", "AWS access key ID", "Google access ID", "pass word"} {
		assignments["multiword "+family] = family + "=" + policyCanary
	}
	for name, assignment := range assignments {
		name, assignment := name, assignment
		t.Run(name, func(t *testing.T) {
			if got := SanitizeString(assignment); strings.Contains(got, policyCanary) || !strings.Contains(got, Marker) {
				t.Fatalf("fragmented assignment sanitized to %q", got)
			}
			durable, err := SanitizeDurableReason("deploy " + assignment + " phase=running")
			if err != nil || strings.Contains(durable, policyCanary) || !strings.Contains(durable, "phase=running") {
				t.Fatalf("durable reason = %q, %v", durable, err)
			}
			branches := map[string]any{
				"list":     wrap(listWith(assignment)),
				"clusters": wrap(clusterWith(assignment)),
				"git":      wrap(gitWith(assignment)),
				"matrix": wrap(map[string]any{"matrix": map[string]any{"generators": []any{
					listWith("phase=running"), gitWith(assignment),
				}}}),
				"merge": wrap(map[string]any{"merge": map[string]any{
					"mergeKeys":  []any{"cluster"},
					"generators": []any{clusterWith("phase=running"), listWith(assignment)},
				}}),
			}
			for branch, payload := range branches {
				if err := ValidateApplicationSetMutation(payload); err == nil {
					t.Errorf("%s accepted %s assignment", branch, name)
				}
			}
		})
	}
}

func TestCanonicalAssignmentScannerMaxSizeIsBoundedAndAllocationStable(t *testing.T) {
	unit := "phase=running "
	dense := strings.Repeat(unit, MaxArgoResponseBodyBytes/len(unit))
	dense += strings.Repeat("x", MaxArgoResponseBodyBytes-len(dense))
	started := time.Now()
	got, found := sanitizeCanonicalAssignments(dense)
	if found || got != dense {
		t.Fatal("dense safe assignments were changed")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("one-pass max-size scan took %s", elapsed)
	}
	if allocations := testing.AllocsPerRun(3, func() {
		_, _ = sanitizeCanonicalAssignments(dense)
	}); allocations > 1 {
		t.Fatalf("dense safe scan allocations = %.1f, want <= 1", allocations)
	}

	credentialDense := strings.Repeat("token=x ", (MaxArgoResponseBodyBytes-len("token=x"))/len("token=x "))
	credentialDense += "token=x"
	if safe, found := sanitizeCanonicalAssignments(credentialDense); !found || safe != Marker {
		t.Fatalf("expanded output did not fail closed within %d-byte bound", maxSanitizedStringLen)
	}
}

func TestCanonicalAssignmentScannerPreservesSafeProse(t *testing.T) {
	for _, prose := range []string{
		`phase=Running message:"deployment complete" ratio=3:1 path=/apps/demo unicode=東京 [maintenance window]`,
		`release status = healthy private key count = 2 api key owner = platform access key rotation = scheduled pass word count = 3`,
	} {
		if got := SanitizeString(prose); got != prose {
			t.Fatalf("safe prose changed: %q", got)
		}
	}
}

func TestStructuredCredentialScanCrossesOneMiBBoundary(t *testing.T) {
	credential := `"clientSecret":"` + policyCanary + `"`
	for name, value := range map[string]string{
		"before boundary": `[` + strings.Repeat("x", maxStructuredStringLen-len(credential)-32) + credential + strings.Repeat("x", 128),
		"after boundary":  `[` + strings.Repeat("x", maxStructuredStringLen+128) + credential,
	} {
		t.Run(name, func(t *testing.T) {
			got := Sanitize(map[string]any{"message": value}).(map[string]any)["message"]
			if got != Marker {
				t.Fatalf("over-limit structured credential was not failed closed")
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
		if err := ValidateApplicationSetMutation(payload); err == nil {
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
		if err := ValidateApplicationSetMutation(payload); err == nil {
			t.Errorf("credential generator scalar %q accepted", scalar)
		}
	}
	if err := ValidateApplicationSetMutation(map[string]any{"spec": map[string]any{"generators": []any{map[string]any{"list": map[string]any{"elements": []any{map[string]any{"credential": "opaque"}}}}}}}); err == nil {
		t.Fatal("singular credential key accepted")
	}
}

func TestValidateApplicationSetClosedGeneratorUnion(t *testing.T) {
	wrap := func(generators ...any) map[string]any {
		return map[string]any{"spec": map[string]any{"generators": generators}}
	}
	list := map[string]any{"list": map[string]any{"elements": []any{map[string]any{"cluster": "prod", "url": "https://kube.example"}}}}
	git := map[string]any{"git": map[string]any{
		"repoURL": "ssh://git@git.example/team/repo.git", "revision": "main",
		"directories": []any{map[string]any{"path": "apps/*", "exclude": false}},
		"values":      map[string]any{"region": "east"},
	}}
	clusters := map[string]any{"clusters": map[string]any{
		"selector": map[string]any{"matchLabels": map[string]any{"environment": "prod"}},
		"values":   map[string]any{"tier": "critical"},
	}}
	matrix := map[string]any{"matrix": map[string]any{"generators": []any{git, list}}}
	merge := map[string]any{"merge": map[string]any{"mergeKeys": []any{"cluster"}, "generators": []any{clusters, matrix}}}
	nestedList := map[string]any{"list": map[string]any{"elements": []any{map[string]any{"x": "y"}}}}
	nestedGit := map[string]any{"git": map[string]any{"repoURL": "https://git.example/repo", "files": []any{map[string]any{"path": "token=" + policyCanary}}}}
	safe := wrap(merge)
	if err := ValidateApplicationSetMutation(safe); err != nil {
		t.Fatalf("safe recursive generator union rejected: %v", err)
	}
	unsafe := map[string]any{
		"unknown":            wrap(map[string]any{"plugin": map[string]any{}}),
		"casing":             wrap(map[string]any{"List": map[string]any{"elements": []any{map[string]any{"x": "y"}}}}),
		"duplicate branches": wrap(map[string]any{"list": map[string]any{"elements": []any{map[string]any{"x": "y"}}}, "git": map[string]any{"repoURL": "https://git.example/repo"}}),
		"list secret":        wrap(map[string]any{"list": map[string]any{"elements": []any{map[string]any{"note": "apiKey=" + policyCanary}}}}),
		"cluster secret":     wrap(map[string]any{"clusters": map[string]any{"values": map[string]any{"privateKey": policyCanary}}}),
		"git secret":         wrap(map[string]any{"git": map[string]any{"repoURL": "https://git.example/repo", "values": map[string]any{"note": "Bearer " + policyCanary}}}),
		"nested secret":      wrap(map[string]any{"matrix": map[string]any{"generators": []any{nestedList, nestedGit}}}),
	}
	for name, payload := range unsafe {
		t.Run(name, func(t *testing.T) {
			if err := ValidateApplicationSetMutation(payload); err == nil {
				t.Fatal("unsafe generator union accepted")
			}
		})
	}
}

func TestGeneratorStructuredScalarsRejectCanonicalCredentialsAcrossBranches(t *testing.T) {
	unsafeJSON := `{"safe":[{"clientSecret":"` + policyCanary + `"}]}`
	unsafeYAML := "safe:\n  - password: " + policyCanary
	malformed := `{"safe":{"client_"secret":"` + policyCanary
	wrap := func(generators ...any) map[string]any {
		return map[string]any{"spec": map[string]any{"generators": generators}}
	}
	listWith := func(value string) any {
		return map[string]any{"list": map[string]any{"elements": []any{map[string]any{"note": value}}}}
	}
	gitWith := func(value string) any {
		return map[string]any{"git": map[string]any{"repoURL": "https://git.example/repo", "values": map[string]any{"note": value}}}
	}
	clusterWith := func(value string) any {
		return map[string]any{"clusters": map[string]any{"values": map[string]any{"note": value}}}
	}
	for name, payload := range map[string]any{
		"list json":     wrap(listWith(unsafeJSON)),
		"clusters yaml": wrap(clusterWith(unsafeYAML)),
		"git malformed": wrap(gitWith(malformed)),
		"matrix nested": wrap(map[string]any{"matrix": map[string]any{"generators": []any{listWith("safe"), gitWith(unsafeJSON)}}}),
		"merge nested":  wrap(map[string]any{"merge": map[string]any{"mergeKeys": []any{"cluster"}, "generators": []any{clusterWith("safe"), listWith(unsafeYAML)}}}),
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateApplicationSetMutation(payload); err == nil {
				t.Fatal("structured generator credential accepted")
			}
		})
	}
	safeStructured := wrap(listWith(`{"safe":[{"note":"diagnostic"}]}`))
	if err := ValidateApplicationSetMutation(safeStructured); err != nil {
		t.Fatalf("safe structured generator scalar rejected: %v", err)
	}
}

func TestValidateMutationRFC6902SourceArraysAndRefs(t *testing.T) {
	safe := []byte(`[
		{"op":"add","path":"/spec/sources/-","value":{"repoURL":"https://git.example/values","ref":"values"}},
		{"op":"add","path":"/spec/sources/-","value":{"repoURL":"https://charts.example/repo","chart":"platform","helm":{"valueFiles":["$values/prod.yaml","defaults.yaml"]}}},
		{"op":"replace","path":"/spec/sources/0/targetRevision","value":"main"},
		{"op":"add","path":"/spec/sources/1/helm/valueFiles/-","value":"$values/region.yaml"},
		{"op":"copy","from":"/spec/sources/0/targetRevision","path":"/spec/sources/1/targetRevision"}
	]`)
	if err := ValidateMutationJSON(safe); err != nil {
		t.Fatalf("safe indexed source patch rejected: %v", err)
	}
	if err := ValidateMutationJSON([]byte(`[{"op":"add","path":"/spec/sources/0/helm/valueFiles/-","value":"defaults/prod.yaml"}]`)); err != nil {
		t.Fatalf("safe relative indexed valueFile rejected: %v", err)
	}
	unsafe := []string{
		`[{"op":"add","path":"/spec/sources/0/helm/values","value":"token=secret"}]`,
		`[{"op":"add","path":"/spec/sources/0/helm/valueFiles/-","value":"$values/../secret.yaml"}]`,
		`[{"op":"add","path":"/spec/sources/0/helm/valueFiles/-","value":"$missing/prod.yaml"}]`,
		`[{"op":"copy","from":"/spec/sources/0/helm/valueFiles/0","path":"/metadata/name"}]`,
		`[{"op":"move","from":"/spec/sources/0","path":"/spec/sources/1"}]`,
		`[{"op":"add","path":"/spec/sources/~2/targetRevision","value":"main"}]`,
		`[{"op":"add","path":"/spec/sources/../targetRevision","value":"main"}]`,
		`[{"op":"add","path":"/spec/sources/0/helm/values~1inline","value":"secret"}]`,
	}
	for _, raw := range unsafe {
		if err := ValidateMutationJSON([]byte(raw)); err == nil {
			t.Errorf("unsafe source patch accepted: %s", raw)
		}
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
