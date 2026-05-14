package middleware

import "testing"

func TestShouldAddTrailingSlash(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// happy path: most API routes without slash
		{"/api/v1/clusters/abc", true},
		{"/api/v1/projects/xyz", true},
		{"/api/v1/users/1/2/3", true},

		// already has slash
		{"/api/v1/clusters/abc/", false},
		{"/api/v1/", false},

		// not under /api/v1/
		{"/helm-repo/astronomer/index.yaml", false},
		{"/healthz", false},
		{"/argocd/applications", false},

		// WS routes — chi.URLParam parses {cluster_id} as the last
		// path component, so the trailing slash would break the
		// match
		{"/api/v1/ws/agent/tunnel/abc-cluster", false},
		{"/api/v1/ws/clusters/abc/shell/sessions/def", false},

		// file extension in the last segment
		{"/api/v1/openapi.yaml", false},
		{"/api/v1/clusters/abc/manifest.yaml", false},
	}
	for _, tc := range cases {
		if got := shouldAddTrailingSlash(tc.path); got != tc.want {
			t.Errorf("shouldAddTrailingSlash(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
