package agenttemplate

import (
	"strings"
	"testing"
)

// argoManagedProfiles are the profiles the baseline ApplicationSets adopt into
// ArgoCD. Keep in sync with the selector in internal/server/baseline_appsets.go
// (astronomer.io/agent-privilege-profile In [operator, admin]).
var argoManagedProfiles = []string{PrivilegeProfileOperator, PrivilegeProfileAdmin}

// TestArgoManagedProfilesGrantClusterWideRead pins the invariant that makes
// ArgoCD adoption bulletproof.
//
// ArgoCD's cluster cache LISTS EVERY RESOURCE TYPE registered in a managed
// cluster — including CRDs installed long after adoption. An enumerated
// allowlist can therefore never be complete: a bare k3d cluster already exposes
// 76 listable types across 23 CRDs. Any type the agent's ClusterRole misses
// fails the entire cache sync:
//
//	ComparisonError: failed to sync cluster ...: failed to load initial state of
//	resource ControllerRevision.apps: controllerrevisions.apps is forbidden:
//	... cannot list resource "controllerrevisions" at the cluster scope
//
// and the failure is silent and total — the ApplicationSet still generates
// Applications, they report health=Healthy, and every one is pinned at
// sync=Unknown forever, deploying nothing.
//
// This was originally "hit controllerrevisions, add controllerrevisions", but
// the error simply moved on to Middleware.traefik.io. Enumeration is the wrong
// shape; cluster-wide READ is the requirement. Writes stay scoped.
func TestArgoManagedProfilesGrantClusterWideRead(t *testing.T) {
	for _, profile := range argoManagedProfiles {
		t.Run(profile, func(t *testing.T) {
			rules := RBACRulesYAML(profile)
			if !grantsClusterWideRead(rules) {
				t.Fatalf("profile %q is adopted into ArgoCD but does not grant cluster-wide read.\n"+
					"ArgoCD's cache lists every registered resource type, so an enumerated allowlist\n"+
					"always eventually misses one (a new CRD is enough) and every Application on the\n"+
					"cluster silently pins at sync=Unknown. Grant:\n"+
					"  - apiGroups: [\"*\"]\n    resources: [\"*\"]\n    verbs: [\"get\", \"list\", \"watch\"]\n"+
					"rules:\n%s", profile, rules)
			}
		})
	}
}

// TestOperatorWildcardIsReadOnly is the containment half of Option A.
//
// operator gains cluster-wide READ so ArgoCD's cache can sync, but it must never
// gain cluster-wide WRITE — that would silently promote every "operator" cluster
// to cluster-admin. admin is deliberately excluded: its wildcard IS write.
func TestOperatorWildcardIsReadOnly(t *testing.T) {
	for _, block := range splitRuleBlocks(RBACRulesYAML(PrivilegeProfileOperator)) {
		if !strings.Contains(block, `apiGroups: ["*"]`) || !strings.Contains(block, `resources: ["*"]`) {
			continue
		}
		for _, w := range []string{`"create"`, `"update"`, `"patch"`, `"delete"`, `"*"`} {
			// The verbs list is the only place these should appear in this block.
			verbs := block[strings.Index(block, "verbs:"):]
			if strings.Contains(verbs, w) {
				t.Errorf("operator's cluster-wide wildcard rule grants write verb %s — that makes the "+
					"profile cluster-admin. The wildcard must stay get/list/watch; scoped writes belong "+
					"on the enumerated rules:\n%s", w, block)
			}
		}
	}
}

// grantsClusterWideRead reports whether some rule allows listing any resource in
// any API group — either an explicit read wildcard or a full wildcard (admin).
func grantsClusterWideRead(rules string) bool {
	for _, block := range splitRuleBlocks(rules) {
		if !strings.Contains(block, `apiGroups: ["*"]`) || !strings.Contains(block, `resources: ["*"]`) {
			continue
		}
		if strings.Contains(block, `verbs: ["*"]`) || strings.Contains(block, `"list"`) {
			return true
		}
	}
	return false
}

// TestControllerRevisionsStayReadOnly guards the least-privilege side: nothing
// needs to write controllerrevisions. They are authored and garbage collected by
// the built-in StatefulSet/DaemonSet controllers — never by us, never by ArgoCD.
func TestControllerRevisionsStayReadOnly(t *testing.T) {
	for _, profile := range []string{
		PrivilegeProfileViewer,
		PrivilegeProfileOperator,
		PrivilegeProfileNamespaceViewer,
		PrivilegeProfileNamespaceOperator,
	} {
		t.Run(profile, func(t *testing.T) {
			for _, block := range splitRuleBlocks(RBACRulesYAML(profile)) {
				if !strings.Contains(block, "controllerrevisions") {
					continue
				}
				for _, w := range []string{`"create"`, `"update"`, `"patch"`, `"delete"`} {
					if strings.Contains(block, w) {
						t.Errorf("profile %q grants %s on controllerrevisions; they are controller-owned "+
							"and garbage collected, so read-only suffices:\n%s", profile, w, block)
					}
				}
			}
		})
	}
}

// splitRuleBlocks splits a rules YAML string into one string per `- apiGroups:`
// rule so verbs can be attributed to the resources they actually apply to.
func splitRuleBlocks(rules string) []string {
	var blocks []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for _, line := range strings.Split(rules, "\n") {
		if strings.Contains(line, "- apiGroups:") {
			flush()
		}
		cur = append(cur, line)
	}
	flush()
	return blocks
}
