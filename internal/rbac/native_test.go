package rbac

import "testing"

func TestNativeAllow(t *testing.T) {
	rules := []NativeRule{
		// Read Certificates in any cluster/namespace.
		{APIGroup: "cert-manager.io", Resource: "certificates", Verbs: []string{"read", "list"}},
		// Full access to widgets, but only in cluster C1, namespace team-a.
		{ClusterID: "C1", Namespace: "team-a", APIGroup: "example.com", Resource: "widgets", Verbs: []string{"*"}},
		// A stored-but-must-be-ignored escalation rule.
		{APIGroup: "rbac.authorization.k8s.io", Resource: "clusterroles", Verbs: []string{"*"}},
		// A stored-but-must-be-ignored exec grant on pods.
		{APIGroup: "", Resource: "pods", Verbs: []string{"exec", "read"}},
	}

	cases := []struct {
		name                               string
		cluster, ns, group, resource, verb string
		want                               bool
	}{
		{"cert read any scope", "C9", "whatever", "cert-manager.io", "certificates", "read", true},
		{"cert list any scope", "C9", "whatever", "cert-manager.io", "certificates", "list", true},
		{"cert delete not granted", "C9", "x", "cert-manager.io", "certificates", "delete", false},
		{"other CRD in same group not granted", "C9", "x", "cert-manager.io", "issuers", "read", false},
		{"widget wildcard verb in scope", "C1", "team-a", "example.com", "widgets", "delete", true},
		{"widget wrong cluster", "C2", "team-a", "example.com", "widgets", "read", false},
		{"widget wrong namespace", "C1", "team-b", "example.com", "widgets", "read", false},
		{"escalation group always refused", "C1", "team-a", "rbac.authorization.k8s.io", "clusterroles", "read", false},
		{"exec verb always refused", "C1", "team-a", "", "pods", "exec", false},
		{"but plain pod read still works via that rule", "C1", "team-a", "", "pods", "read", true},
		{"logs verb always refused", "C9", "x", "cert-manager.io", "certificates", "logs", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NativeAllow(rules, c.cluster, c.ns, c.group, c.resource, c.verb); got != c.want {
				t.Fatalf("NativeAllow(%s/%s/%s/%s verb=%s) = %v, want %v",
					c.cluster, c.ns, c.group, c.resource, c.verb, got, c.want)
			}
		})
	}

	// Empty ruleset never allows.
	if NativeAllow(nil, "C1", "n", "g", "r", "read") {
		t.Fatal("nil ruleset must deny")
	}
}
