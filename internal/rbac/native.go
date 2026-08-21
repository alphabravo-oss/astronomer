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

// NativeRulesFromBindings extracts CRD grants stored on bound cluster/project
// roles and scopes each one to the binding's cluster/namespace. Global
// bindings contribute grants with empty cluster/namespace (any scope).
func NativeRulesFromBindings(bindings []RoleBinding) []NativeRule {
	var out []NativeRule
	for _, b := range bindings {
		if b.IsSuperuser {
			continue
		}
		for _, rule := range b.RoleRules {
			if !rule.IsCRDGrant() {
				continue
			}
			verbs := NormalizeNativeVerbs(rule.Verbs)
			if len(verbs) == 0 {
				continue
			}
			for _, group := range rule.CRDAPIGroups() {
				for _, resource := range rule.ResourceNames() {
					if resource == "" {
						continue
					}
					out = append(out, NativeRule{
						ClusterID: b.ClusterID,
						Namespace: b.Namespace,
						APIGroup:  group,
						Resource:  resource,
						Verbs:     verbs,
					})
				}
			}
		}
	}
	return out
}

// NormalizeNativeVerbs maps Kubernetes PolicyRule verbs onto the coarse
// vocabulary NativeAllow understands (get→read, patch→update) and drops
// exec/logs so they cannot sneak in through a role-folded grant.
func NormalizeNativeVerbs(verbs []string) []string {
	if len(verbs) == 0 {
		return nil
	}
	out := make([]string, 0, len(verbs))
	seen := make(map[string]struct{}, len(verbs))
	for _, raw := range verbs {
		v := strings.ToLower(strings.TrimSpace(raw))
		switch v {
		case "get":
			v = string(VerbRead)
		case "patch":
			v = string(VerbUpdate)
		case string(VerbExec), string(VerbLogs), "proxy":
			continue
		}
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func verbMatches(verbs []string, verb string) bool {
	for _, v := range verbs {
		if v == "*" || v == verb {
			return true
		}
	}
	return false
}
