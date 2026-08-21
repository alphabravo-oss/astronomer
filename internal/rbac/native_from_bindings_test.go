package rbac

import "testing"

func TestNativeRulesFromBindings(t *testing.T) {
	bindings := []RoleBinding{
		{
			ClusterID: "C1",
			Namespace: "team-a",
			RoleRules: []Rule{
				{Resource: "workloads", Verbs: []string{"read"}},
				{Resource: "certificates", APIGroups: []string{"cert-manager.io"}, Verbs: []string{"get", "list"}},
			},
		},
		{
			RoleRules: []Rule{
				{Resource: "issuers", APIGroupsCamel: []string{"cert-manager.io"}, Verbs: []string{"read"}},
			},
		},
	}

	got := NativeRulesFromBindings(bindings)
	if len(got) != 2 {
		t.Fatalf("got %d native rules, want 2: %+v", len(got), got)
	}

	cert := got[0]
	if cert.ClusterID != "C1" || cert.Namespace != "team-a" || cert.APIGroup != "cert-manager.io" || cert.Resource != "certificates" {
		t.Fatalf("first rule = %+v", cert)
	}
	if !verbMatches(cert.Verbs, "read") || !verbMatches(cert.Verbs, "list") {
		t.Fatalf("get should normalize to read; verbs=%v", cert.Verbs)
	}

	issuer := got[1]
	if issuer.ClusterID != "" || issuer.APIGroup != "cert-manager.io" || issuer.Resource != "issuers" {
		t.Fatalf("second rule = %+v", issuer)
	}
}

func TestMatchRuleIgnoresCRDGrants(t *testing.T) {
	e := NewEngine()
	crd := Rule{Resource: "custom_resources", APIGroups: []string{"cert-manager.io"}, Verbs: []string{"*"}}
	if e.matchRule(crd, ResourceCustomResources, VerbRead) {
		t.Fatal("CRD grant must not satisfy a coarse custom_resources check")
	}
	coarse := Rule{Resource: "custom_resources", Verbs: []string{"read"}}
	if !e.matchRule(coarse, ResourceCustomResources, VerbRead) {
		t.Fatal("coarse custom_resources:read should still match")
	}
}
