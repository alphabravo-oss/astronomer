package audit

import "testing"

func TestClassifyActionClass(t *testing.T) {
	cases := []struct {
		action, source, stored, want string
	}{
		{"auth.login", "service", "", ClassAuth},
		{"auth.refresh", "service", "mutation", ClassAuth},
		{"sso.login", "http", "", ClassAuth},
		{"read.audit", "http", "read", ClassRead},
		{"read.audit", "http", "", ClassRead},
		{"charlie.http.read", "http", "", ClassRead},
		{"agent.connected", "tunnel", "", ClassSystem},
		{"agent.token.rotated", "tunnel", "mutation", ClassSystem},
		{"catalog.repo.sync_failed", "worker", "", ClassSystem},
		{"role.create", "service", "", ClassMutation},
		{"request.post", "http", "mutation", ClassMutation},
		{"cluster.delete", "http", "mutation", ClassMutation},
		{"anything", "worker", "mutation", ClassSystem},
	}
	for _, tc := range cases {
		got := ClassifyActionClass(tc.action, tc.source, tc.stored)
		if got != tc.want {
			t.Errorf("ClassifyActionClass(%q, %q, %q) = %q, want %q",
				tc.action, tc.source, tc.stored, got, tc.want)
		}
	}
}
