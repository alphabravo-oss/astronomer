package server

import "testing"

func TestIsPrivilegeEscalationAPIGroup_MatchesNativeDenylist(t *testing.T) {
	// SEC-04: proxy gate must include CSR + token-mint groups that native
	// RBAC already treats as privilege escalation.
	must := []string{
		"rbac.authorization.k8s.io",
		"admissionregistration.k8s.io",
		"apiregistration.k8s.io",
		"apiextensions.k8s.io",
		"certificates.k8s.io",
		"authentication.k8s.io",
		"Certificates.k8s.io", // case fold
	}
	for _, g := range must {
		if !isPrivilegeEscalationAPIGroup(g) {
			t.Errorf("isPrivilegeEscalationAPIGroup(%q) = false, want true", g)
		}
	}
	if isPrivilegeEscalationAPIGroup("apps") {
		t.Error("apps must not be treated as privilege-escalation group")
	}
}
