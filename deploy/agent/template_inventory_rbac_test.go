package agenttemplate

import (
	"strings"
	"testing"
)

var inventoryProfiles = []string{PrivilegeProfileOperator, PrivilegeProfileAdmin}

// TestInventoryProfilesGrantClusterWideRead pins complete discovery for CRDs
// installed after adoption. Writes remain scoped by explicit rules.
func TestInventoryProfilesGrantClusterWideRead(t *testing.T) {
	for _, profile := range inventoryProfiles {
		t.Run(profile, func(t *testing.T) {
			rules := RBACRulesYAML(profile)
			if !grantsClusterWideRead(rules) {
				t.Fatalf("inventory profile %q does not grant cluster-wide read.\n"+
					"An enumerated allowlist eventually misses a newly installed CRD. Grant:\n"+
					"  - apiGroups: [\"*\"]\n    resources: [\"*\"]\n    verbs: [\"get\", \"list\", \"watch\"]\n"+
					"rules:\n%s", profile, rules)
			}
		})
	}
}

// TestOperatorWildcardIsReadOnly is the containment half of Option A.
//
// operator gains cluster-wide READ for inventory, but it must never
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
// the built-in StatefulSet/DaemonSet controllers, never by the agent.
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
