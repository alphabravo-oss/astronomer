// Package deploy — chart-render coverage for the management-cluster agent
// ServiceAccount ClusterRole (C1 / B3 in the agent-authz security review).
//
// The ClusterRole used to be an unconditional `*/*/*` + nonResourceURLs:["*"]
// cluster-admin grant with no values knob. These tests are the negative gate:
// they fail if a blanket wildcard rule ever comes back, and they prove the
// additive Helm values overrides work.
package deploy

import (
	"os"
	"path/filepath"
	"testing"
)

// agentClusterRoleRules pulls the rules slice out of the rendered management
// agent ClusterRole (named after the release: "astronomer").
func agentClusterRoleRules(t *testing.T, out string) []any {
	t.Helper()
	docs := parseRenderedDocs(t, out)
	role := findRenderedDoc(t, docs, "ClusterRole", "astronomer")
	rules, ok := role["rules"].([]any)
	if !ok || len(rules) == 0 {
		t.Fatalf("agent ClusterRole has no rules: %#v", role)
	}
	return rules
}

// isAllWildcard reports whether a rule grants apiGroups:["*"] AND
// resources:["*"] AND verbs:["*"] — the cluster-admin shape we banned.
func isAllWildcard(rule map[string]any) bool {
	return onlyWildcard(rule["apiGroups"]) &&
		onlyWildcard(rule["resources"]) &&
		onlyWildcard(rule["verbs"])
}

// onlyWildcard reports whether a YAML list value is exactly ["*"].
func onlyWildcard(v any) bool {
	list, ok := v.([]any)
	if !ok || len(list) != 1 {
		return false
	}
	return stringValue(list[0]) == "*"
}

func containsString(v any, want string) bool {
	list, ok := v.([]any)
	if !ok {
		return false
	}
	for _, item := range list {
		if stringValue(item) == want {
			return true
		}
	}
	return false
}

func TestAgentClusterRoleHasNoWildcardRule(t *testing.T) {
	for _, frontend := range []string{"true", "false"} {
		frontend := frontend
		t.Run("frontend.enabled="+frontend, func(t *testing.T) {
			out := helmTemplate(t, "frontend.enabled="+frontend)
			for i, raw := range agentClusterRoleRules(t, out) {
				rule, ok := raw.(map[string]any)
				if !ok {
					t.Fatalf("rule %d is not a map: %#v", i, raw)
				}
				if isAllWildcard(rule) {
					t.Fatalf("agent ClusterRole rule %d is a banned */*/* cluster-admin grant: %#v", i, rule)
				}
				if containsString(rule["nonResourceURLs"], "*") {
					t.Fatalf("agent ClusterRole rule %d grants nonResourceURLs:[\"*\"]: %#v", i, rule)
				}
			}
		})
	}
}

func TestAgentClusterRoleGrantsExpectedCoreAccess(t *testing.T) {
	out := helmTemplate(t)
	rules := agentClusterRoleRules(t, out)

	// Sanity: the allowlist still actually grants the irreducible core the
	// platform needs, so a future over-tightening that breaks install is
	// caught here rather than in production.
	wantResources := []string{"pods/exec", "secrets", "customresourcedefinitions"}
	found := map[string]bool{}
	for _, raw := range rules {
		rule, _ := raw.(map[string]any)
		for _, res := range wantResources {
			if containsString(rule["resources"], res) {
				found[res] = true
			}
		}
	}
	for _, res := range wantResources {
		if !found[res] {
			t.Fatalf("agent ClusterRole missing expected resource %q:\n%s", res, out)
		}
	}
}

func TestAgentClusterRoleExtraRulesOverrideIsAdditive(t *testing.T) {
	valuesPath := filepath.Join(t.TempDir(), "extra-rules.yaml")
	values := []byte(`
serviceAccount:
  extraRules:
    - apiGroups: ["example.com"]
      resources: ["widgets"]
      verbs: ["get", "list", "watch"]
  extraNonResourceRules:
    - nonResourceURLs: ["/custom-health"]
      verbs: ["get"]
`)
	if err := os.WriteFile(valuesPath, values, 0o600); err != nil {
		t.Fatalf("write values override: %v", err)
	}

	out := helmTemplateWithValueFiles(t, []string{valuesPath})
	rules := agentClusterRoleRules(t, out)

	// The operator override is present...
	var sawExtraResource, sawExtraNonResource bool
	for _, raw := range rules {
		rule, _ := raw.(map[string]any)
		if containsString(rule["apiGroups"], "example.com") && containsString(rule["resources"], "widgets") {
			sawExtraResource = true
		}
		if containsString(rule["nonResourceURLs"], "/custom-health") {
			sawExtraNonResource = true
		}
		// ...and it did not reintroduce the wildcard.
		if rule, ok := raw.(map[string]any); ok && isAllWildcard(rule) {
			t.Fatalf("override reintroduced a */*/* rule: %#v", rule)
		}
	}
	if !sawExtraResource {
		t.Fatalf("serviceAccount.extraRules override did not render:\n%s", out)
	}
	if !sawExtraNonResource {
		t.Fatalf("serviceAccount.extraNonResourceRules override did not render:\n%s", out)
	}

	// ...and the base allowlist is still there (additive, not replaced).
	var sawBase bool
	for _, raw := range rules {
		rule, _ := raw.(map[string]any)
		if containsString(rule["resources"], "pods/exec") {
			sawBase = true
		}
	}
	if !sawBase {
		t.Fatalf("override replaced the base allowlist instead of extending it:\n%s", out)
	}
}
