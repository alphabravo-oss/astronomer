package rbac

import "strings"

// NativeRule is one fine-grained, ADDITIVE allow entry for a user: it grants a
// set of verbs on an exact (apiGroup, resource), optionally narrowed to a
// cluster and/or namespace. It complements the coarse rule model — the
// k8s-proxy authz hook consults native rules ONLY after the coarse check has
// denied, so a native rule can only ever grant access an operator explicitly
// authored.
//
// Zero-value scope fields are wildcards WITHIN the request's own path scope:
//
//	ClusterID "" -> any cluster; Namespace "" -> any namespace.
//	Resource "*" -> any resource in the group; a "*" in Verbs -> any verb.
type NativeRule struct {
	ClusterID string
	Namespace string
	APIGroup  string
	Resource  string
	Verbs     []string
}

// isPrivilegeEscalationGroup mirrors the proxy's escalation-group list. Native
// rules must NEVER be able to grant these — writing them is how a caller
// escalates to cluster-admin, so they stay behind an explicit coarse
// ResourceRBAC grant. Keep this in sync with the proxy's
// isPrivilegeEscalationAPIGroup.
func isPrivilegeEscalationGroup(group string) bool {
	switch strings.ToLower(strings.TrimSpace(group)) {
	case "rbac.authorization.k8s.io",
		"admissionregistration.k8s.io",
		"apiregistration.k8s.io",
		"apiextensions.k8s.io",
		// certificates.k8s.io: approving/signing a CertificateSigningRequest
		// mints an arbitrary client cert (e.g. CN=system:masters), which is
		// cluster-admin-equivalent. A native rule granting CSR verbs must never
		// be honored — keep it behind an explicit coarse grant.
		"certificates.k8s.io",
		// authentication.k8s.io: TokenRequest / TokenReview can mint or validate
		// bearer tokens for other identities; treat as escalation.
		"authentication.k8s.io":
		return true
	}
	return false
}

// NativeAllow reports whether any rule grants (apiGroup, resource, verb) at the
// given cluster/namespace. It is deliberately conservative:
//
//   - It refuses privilege-escalation api groups outright, so a stored rule on
//     rbac.authorization.k8s.io (however it got there) can never grant access.
//   - It refuses the high-risk "exec" and "logs" verbs, so native rules can
//     never open a pod shell or stream logs — those keep requiring a coarse
//     pods:exec / pods:logs grant.
//
// verb is the coarse rbac.Verb string (read|list|watch|create|update|delete).
func NativeAllow(rules []NativeRule, clusterID, namespace, apiGroup, resource, verb string) bool {
	if isPrivilegeEscalationGroup(apiGroup) {
		return false
	}
	if verb == string(VerbExec) || verb == string(VerbLogs) {
		return false
	}
	for _, r := range rules {
		if r.ClusterID != "" && r.ClusterID != clusterID {
			continue
		}
		if r.Namespace != "" && r.Namespace != namespace {
			continue
		}
		if r.APIGroup != apiGroup {
			continue
		}
		if r.Resource != "*" && r.Resource != resource {
			continue
		}
		if verbMatches(r.Verbs, verb) {
			return true
		}
	}
	return false
}

func verbMatches(verbs []string, verb string) bool {
	for _, v := range verbs {
		if v == "*" || v == verb {
			return true
		}
	}
	return false
}
