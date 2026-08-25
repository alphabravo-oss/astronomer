package builtinbundles

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseBuilderSeparatesMultiSourceArchivesAndPreservesLegacyLayout(t *testing.T) {
	for _, tool := range []string{"bash", "go", "gzip", "jq", "sha256sum", "tar"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is unavailable: %v", tool, err)
		}
	}
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	secondURL := "https://charts.example.test/stable"
	catalog.Components[1].Source.URL = secondURL
	for index := range catalog.Components {
		payload := fakeChartPayload(catalog.Components[index].Source.Chart, catalog.Components[index].Source.Version)
		digest := sha256.Sum256(payload)
		catalog.Components[index].Source.ChartDigest = fmt.Sprintf("sha256:%x", digest)
	}
	tempDir := t.TempDir()
	catalogPath := filepath.Join(tempDir, "catalog.json")
	catalogJSON, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogPath, catalogJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(tempDir, "bin")
	if err := os.Mkdir(fakeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	helmPath := filepath.Join(fakeBin, "helm")
	helm := `#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == pull ]]
chart="$2"
shift 2
while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo) shift 2 ;;
    --version) version="$2"; shift 2 ;;
    --destination) destination="$2"; shift 2 ;;
    *) exit 2 ;;
  esac
done
printf '%s' "${chart}:${version}" >"${destination}/${chart}-${version}.tgz"
`
	if err := os.WriteFile(helmPath, []byte(helm), 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(tempDir, "bundles.tar.gz")
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "build-builtin-bundles.sh"), "--output", output)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "BUNDLE_CATALOG="+catalogPath, "PATH="+fakeBin+":"+os.Getenv("PATH"))
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("multi-source release builder failed: %v\n%s", err, combined)
	}
	listing, err := exec.Command("tar", "-tzf", output).Output()
	if err != nil {
		t.Fatal(err)
	}
	entries := string(listing)
	legacy := "charts/" + catalog.Components[0].Source.Chart + "-" + catalog.Components[0].Source.Version + ".tgz"
	sourceDigest := sha256.Sum256([]byte(secondURL))
	multi := fmt.Sprintf("charts/sources/%x/%s-%s.tgz", sourceDigest[:8], catalog.Components[1].Source.Chart, catalog.Components[1].Source.Version)
	for _, want := range []string{legacy, multi} {
		if !strings.Contains(entries, want) {
			t.Errorf("archive listing does not contain %q:\n%s", want, entries)
		}
	}
}

func fakeChartPayload(chart, version string) []byte {
	return []byte(chart + ":" + version)
}
