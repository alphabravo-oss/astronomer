package deploy

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

var chartVersionLine = regexp.MustCompile(`(?m)^version:\s*"?([^"\s]+)"?\s*$`)

// chartVersion returns deploy/chart/Chart.yaml's version so render tests do not
// pin a release number that every version bump would have to edit.
func chartVersion(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("chart", "Chart.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m := chartVersionLine.FindSubmatch(data)
	if m == nil {
		t.Fatal("deploy/chart/Chart.yaml has no version line")
	}
	return string(m[1])
}
