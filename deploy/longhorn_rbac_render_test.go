package deploy

import "testing"

func TestLocalLonghornLifecyclePermissionsAreBounded(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t))
	role := findRenderedDoc(t, docs, "ClusterRole", "astronomer")
	var rules []any
	for _, raw := range role["rules"].([]any) {
		rule := raw.(map[string]any)
		for _, group := range stringListValue(rule["apiGroups"]) {
			if group == "longhorn.io" {
				rules = append(rules, rule)
			}
		}
	}
	role["rules"] = rules
	assertExactRBACRules(t, role, []rbacRuleContract{
		{apiGroups: []string{"longhorn.io"}, resources: []string{"nodes", "volumes"}, verbs: []string{"get", "list", "watch"}},
		{apiGroups: []string{"longhorn.io"}, resources: []string{"settings"}, resourceNames: []string{"deleting-confirmation-flag"}, verbs: []string{"get", "patch"}},
		{apiGroups: []string{"longhorn.io"}, resources: []string{"settings"}, verbs: []string{"create"}},
	})
}
