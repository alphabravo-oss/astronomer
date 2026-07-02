package rbac

import "testing"

// TestNativeAllow_RefusesCertAndTokenMintingGroups pins finding 2: a stored
// native rule on certificates.k8s.io (CSR approval → mint a system:masters
// client cert) or authentication.k8s.io (token minting) must NEVER grant
// access, regardless of how the rule got persisted.
func TestNativeAllow_RefusesCertAndTokenMintingGroups(t *testing.T) {
	rules := []NativeRule{
		{APIGroup: "certificates.k8s.io", Resource: "certificatesigningrequests", Verbs: []string{"*"}},
		{APIGroup: "certificates.k8s.io", Resource: "*", Verbs: []string{"update", "create"}},
		{APIGroup: "authentication.k8s.io", Resource: "tokenrequests", Verbs: []string{"create"}},
	}

	cases := []struct {
		name                  string
		group, resource, verb string
	}{
		{"csr approve refused", "certificates.k8s.io", "certificatesigningrequests", "update"},
		{"csr create refused", "certificates.k8s.io", "certificatesigningrequests", "create"},
		{"csr subresource wildcard refused", "certificates.k8s.io", "certificatesigningrequests", "read"},
		{"token mint refused", "authentication.k8s.io", "tokenrequests", "create"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if NativeAllow(rules, "C1", "ns", c.group, c.resource, c.verb) {
				t.Fatalf("NativeAllow must refuse escalation group %s/%s verb=%s", c.group, c.resource, c.verb)
			}
		})
	}

	// isPrivilegeEscalationGroup should classify the new groups directly.
	for _, g := range []string{"certificates.k8s.io", "authentication.k8s.io", "CERTIFICATES.K8S.IO", " certificates.k8s.io "} {
		if !isPrivilegeEscalationGroup(g) {
			t.Fatalf("isPrivilegeEscalationGroup(%q) = false, want true", g)
		}
	}

	// A non-escalation CRD group is still allowed when a rule grants it, proving
	// the refusal is targeted and did not over-broaden.
	ok := []NativeRule{{APIGroup: "cert-manager.io", Resource: "certificates", Verbs: []string{"read"}}}
	if !NativeAllow(ok, "C1", "ns", "cert-manager.io", "certificates", "read") {
		t.Fatal("cert-manager.io (a normal CRD group) read must still be grantable")
	}
}
