package docs

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestMarketingScopeIsAdoptOnly guards against MARKETING.md reintroducing the
// false claim that Astronomer provisions clusters. The product is day-2/adopt
// only, consistent with README.md ("Astronomer is not a cluster provisioning
// product").
func TestMarketingScopeIsAdoptOnly(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file location")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	data, err := os.ReadFile(filepath.Join(repoRoot, "MARKETING.md"))
	if err != nil {
		t.Fatalf("reading MARKETING.md: %v", err)
	}
	content := strings.ToLower(string(data))

	for _, bad := range []string{"provisions new ones", "provisions new clusters", "provision new clusters"} {
		if strings.Contains(content, bad) {
			t.Errorf("MARKETING.md contains false provisioning claim %q; Astronomer is adopt/day-2 only", bad)
		}
	}
}
