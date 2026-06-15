package rbac

import (
	"os"
	"strings"
	"testing"
)

func TestRoleTemplatesUseCanonicalRBACVocabulary(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	for _, tmpl := range catalog.All() {
		for i, rule := range tmpl.Rules {
			if !IsCanonicalResource(rule.Resource) {
				t.Fatalf("%s rules[%d] uses non-canonical resource %q", tmpl.Name, i, rule.Resource)
			}
			for j, verb := range rule.Verbs {
				if !IsCanonicalVerb(verb) {
					t.Fatalf("%s rules[%d].verbs[%d] uses non-canonical verb %q", tmpl.Name, i, j, verb)
				}
			}
		}
	}
}

func TestRBACContractDocCoversCanonicalVocabulary(t *testing.T) {
	raw, err := os.ReadFile("../../docs/rbac-permission-contract.md")
	if err != nil {
		t.Fatalf("read RBAC contract doc: %v", err)
	}
	doc := string(raw)
	for _, resource := range CanonicalResources() {
		token := "`" + string(resource) + "`"
		if !strings.Contains(doc, token) {
			t.Fatalf("RBAC contract doc missing resource %s", token)
		}
	}
	for _, verb := range CanonicalVerbs() {
		token := "`" + string(verb) + "`"
		if !strings.Contains(doc, token) {
			t.Fatalf("RBAC contract doc missing verb %s", token)
		}
	}
}

func TestClusterExplorerRoleTemplatesCoverCommonResourceFamilies(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}

	requireTemplateRules(t, catalog, "cluster-viewer", []Rule{
		{Resource: string(ResourceNodes), Verbs: []string{string(VerbRead), string(VerbList)}},
		{Resource: string(ResourceServices), Verbs: []string{string(VerbRead), string(VerbList)}},
		{Resource: string(ResourceIngresses), Verbs: []string{string(VerbRead), string(VerbList)}},
		{Resource: string(ResourceStorage), Verbs: []string{string(VerbRead), string(VerbList)}},
		{Resource: string(ResourceConfigMaps), Verbs: []string{string(VerbRead), string(VerbList)}},
		{Resource: string(ResourceNetworkPolicies), Verbs: []string{string(VerbRead), string(VerbList)}},
	})
	requireTemplateRules(t, catalog, "cluster-operator", []Rule{
		{Resource: string(ResourceNodes), Verbs: []string{string(VerbRead), string(VerbList)}},
		{Resource: string(ResourceServices), Verbs: []string{string(VerbCreate), string(VerbRead), string(VerbUpdate), string(VerbDelete), string(VerbList)}},
		{Resource: string(ResourceIngresses), Verbs: []string{string(VerbCreate), string(VerbRead), string(VerbUpdate), string(VerbDelete), string(VerbList)}},
		{Resource: string(ResourceStorage), Verbs: []string{string(VerbCreate), string(VerbRead), string(VerbUpdate), string(VerbDelete), string(VerbList)}},
		{Resource: string(ResourceConfigMaps), Verbs: []string{string(VerbCreate), string(VerbRead), string(VerbUpdate), string(VerbDelete), string(VerbList)}},
		{Resource: string(ResourceNetworkPolicies), Verbs: []string{string(VerbCreate), string(VerbRead), string(VerbUpdate), string(VerbDelete), string(VerbList)}},
	})
}

func requireTemplateRules(t *testing.T, catalog *Catalog, name string, required []Rule) {
	t.Helper()
	tmpl, ok := catalog.Get(name)
	if !ok {
		t.Fatalf("template %q not found", name)
	}
	for _, want := range required {
		var got *Rule
		for i := range tmpl.Rules {
			if tmpl.Rules[i].Resource == want.Resource {
				got = &tmpl.Rules[i]
				break
			}
		}
		if got == nil {
			t.Fatalf("%s missing resource %s", name, want.Resource)
		}
		for _, verb := range want.Verbs {
			if !containsString(got.Verbs, verb) {
				t.Fatalf("%s resource %s missing verb %s; got %v", name, want.Resource, verb, got.Verbs)
			}
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
