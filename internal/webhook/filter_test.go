package webhook

import "testing"

func TestFilterMatch_GlobsAndExact(t *testing.T) {
	cases := []struct {
		name    string
		event   string
		filters []string
		want    bool
	}{
		{
			name:    "empty filter list matches everything",
			event:   "audit.user.login",
			filters: nil,
			want:    true,
		},
		{
			name:    "empty string filter matches everything",
			event:   "audit.user.login",
			filters: []string{""},
			want:    true,
		},
		{
			name:    "exact match",
			event:   "auth.login_failed",
			filters: []string{"auth.login_failed"},
			want:    true,
		},
		{
			name:    "exact mismatch",
			event:   "auth.login_failed",
			filters: []string{"auth.login_succeeded"},
			want:    false,
		},
		{
			name:    "prefix glob",
			event:   "audit.cluster.created",
			filters: []string{"audit.*"},
			want:    true,
		},
		{
			name:    "middle glob",
			event:   "cluster.decommission.started",
			filters: []string{"cluster.*.started"},
			want:    true,
		},
		{
			name:    "question mark single-char",
			event:   "x.a",
			filters: []string{"x.?"},
			want:    true,
		},
		{
			name:    "question mark insufficient",
			event:   "x.ab",
			filters: []string{"x.?"},
			want:    false,
		},
		{
			name:    "comma-separated alternatives — first match",
			event:   "auth.login_failed",
			filters: []string{"audit.*, auth.login_failed"},
			want:    true,
		},
		{
			name:    "comma-separated alternatives — second match",
			event:   "cluster.created",
			filters: []string{"audit.*, cluster.*"},
			want:    true,
		},
		{
			name:    "comma-separated alternatives — no match",
			event:   "user.deleted",
			filters: []string{"audit.*, cluster.*"},
			want:    false,
		},
		{
			name:    "multiple filter entries — second matches",
			event:   "cluster.k8s_changed",
			filters: []string{"audit.*", "cluster.*"},
			want:    true,
		},
		{
			name:    "trailing-star matches empty suffix",
			event:   "x",
			filters: []string{"x*"},
			want:    true,
		},
		{
			name:    "leading-star matches everything",
			event:   "anything",
			filters: []string{"*"},
			want:    true,
		},
		{
			name:    "consecutive stars are equivalent to one star",
			event:   "abc.def",
			filters: []string{"a**f"},
			want:    true,
		},
		{
			name:    "star does not match across nothing-to-match",
			event:   "",
			filters: []string{"a*"},
			want:    false,
		},
		{
			name:    "no wildcard means exact",
			event:   "auth.login",
			filters: []string{"auth.lo"},
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MatchFilters(tc.event, tc.filters)
			if got != tc.want {
				t.Errorf("MatchFilters(%q, %v) = %v; want %v", tc.event, tc.filters, got, tc.want)
			}
		})
	}
}
