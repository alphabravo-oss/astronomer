package compatibility

import "testing"

func TestSupportsKubernetes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		version string
		want    bool
	}{
		{"v1.33.0", true}, {"1.34.12", true}, {"v1.35.7+k3s1", true},
		{"v1.30.4+k3s1", false}, {"1.32.99", false}, {"1.36.0", false}, {"2.34.0", false},
		{"", false}, {"1.35", false}, {"v1.35.0-rc.1", false}, {"1.35.0-beta.1", false}, {" 1.35.0", false}, {"banana", false},
	} {
		t.Run(tc.version, func(t *testing.T) {
			t.Parallel()
			if got := SupportsKubernetes(tc.version); got != tc.want {
				t.Fatalf("SupportsKubernetes(%q)=%v want %v", tc.version, got, tc.want)
			}
		})
	}
}
