package deploy

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseManifestIsValidatedAndMountedByChart(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skipf("helm unavailable: %v", err)
	}
	path := filepath.Join(t.TempDir(), "release-manifest.json")
	payload := `{"schema_version":1,"release":{"version":"v1.0.0"}}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	output, stderr, err := renderChartWithReleaseManifest(path)
	if err != nil {
		t.Fatalf("render release manifest: %v\n%s", err, stderr)
	}
	for _, required := range []string{
		"name: astronomer-release-manifest",
		"release-manifest.json: |",
		payload,
		"name: RELEASE_MANIFEST_PATH",
		"value: /etc/astronomer/release/release-manifest.json",
		"mountPath: /etc/astronomer/release",
	} {
		if !strings.Contains(output, required) {
			t.Errorf("rendered release contract is missing %q", required)
		}
	}
	if got := strings.Count(output, "name: RELEASE_MANIFEST_PATH"); got != 2 {
		t.Errorf("RELEASE_MANIFEST_PATH count = %d, want server and worker", got)
	}

	mappingPath := filepath.Join(t.TempDir(), "mirror-mapping.json")
	mapping := `{"schema_version":1,"release_version":"v1.0.0"}`
	if err := os.WriteFile(mappingPath, []byte(mapping), 0o600); err != nil {
		t.Fatal(err)
	}
	output, stderr, err = renderChartWithReleaseManifest(path, mappingPath)
	if err != nil {
		t.Fatalf("render release mirror mapping: %v\n%s", err, stderr)
	}
	for _, required := range []string{
		"mirror-mapping.json: |",
		mapping,
		"name: RELEASE_MIRROR_MAPPING_PATH",
		"value: /etc/astronomer/release/mirror-mapping.json",
		"path: mirror-mapping.json",
	} {
		if !strings.Contains(output, required) {
			t.Errorf("rendered mirror contract is missing %q", required)
		}
	}
	if got := strings.Count(output, "name: RELEASE_MIRROR_MAPPING_PATH"); got != 2 {
		t.Errorf("RELEASE_MIRROR_MAPPING_PATH count = %d, want server and worker", got)
	}
	if got := strings.Count(output, "path: mirror-mapping.json"); got != 2 {
		t.Errorf("mirror-mapping.json volume item count = %d, want server and worker", got)
	}

	if err := os.WriteFile(path, []byte(`{"schema_version":1,"release":{"version":"v1.0.1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, err = renderChartWithReleaseManifest(path)
	if err == nil || !strings.Contains(stderr, "does not match chart") {
		t.Fatalf("mismatched release manifest error = %v, stderr=%s", err, stderr)
	}
}

func renderChartWithReleaseManifest(path string, mappingPath ...string) (string, string, error) {
	arguments := []string{
		"helm", "template", "astronomer", "chart", "-f", "chart/values.yaml",
		"--set", testRenderSecretKeySet,
		"--set", testRenderEncryptionKeySet,
		"--set-file", "release.manifest=" + path,
	}
	if len(mappingPath) == 1 {
		arguments = append(arguments, "--set-file", "release.mirrorMapping="+mappingPath[0])
	}
	command := exec.Command(arguments[0], arguments[1:]...)
	command.Dir = "."
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.String(), stderr.String(), err
}
