package deploy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestChartBaseValuesAreProductionSafe(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	base, err := os.ReadFile(filepath.Join(filepath.Dir(here), "chart", "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(base)
	for _, required := range []string{"env: production", `debug: "false"`, `allowedHosts: ""`} {
		if !strings.Contains(text, required) {
			t.Fatalf("base values missing production-safe default %q", required)
		}
	}
}

func TestChartDevelopmentProfileIsExplicit(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dev, err := os.ReadFile(filepath.Join(filepath.Dir(here), "chart", "values-dev.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(dev)
	for _, required := range []string{"env: development", `debug: "true"`, `allowedHosts: "*"`} {
		if !strings.Contains(text, required) {
			t.Fatalf("development values missing explicit override %q", required)
		}
	}
}
