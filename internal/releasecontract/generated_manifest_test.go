package releasecontract

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplicationImageInventoryByRelease(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		includeDR     bool
		replaceWorker string
		wantValid     bool
	}{
		{name: "legacy", version: "v1.1.0", wantValid: true},
		{name: "current", version: "v1.2.0", includeDR: true, wantValid: true},
		{name: "patch", version: "v1.2.1", includeDR: true, wantValid: true},
		{name: "future major", version: "v2.0.0", includeDR: true, wantValid: true},
		{name: "missing DR", version: "v1.2.0"},
		{name: "unexpected legacy DR", version: "v1.1.0", includeDR: true},
		{name: "duplicate", version: "v1.2.0", includeDR: true, replaceWorker: "server"},
		{name: "unknown", version: "v1.2.0", includeDR: true, replaceWorker: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := validManifest()
			manifest.Release.Version = tc.version
			manifest.Release.SigningPolicy.CertificateIdentity = strings.TrimSuffix(manifest.Release.SigningPolicy.CertificateIdentity, "v1.0.0") + tc.version
			if tc.replaceWorker != "" {
				manifest.Astronomer.Images[5] = testArtifact(tc.replaceWorker, 'c')
			}
			if tc.includeDR {
				manifest.Astronomer.Images = append(manifest.Astronomer.Images, testArtifact("dr", 'd'))
			}
			_, err := manifest.Validate(tc.version)
			if (err == nil) != tc.wantValid {
				t.Fatalf("Validate(%s) = %v; want valid=%v", tc.version, err, tc.wantValid)
			}
		})
	}
}

// Exercise the producer and the application loader together: independent
// generator and loader fixtures previously disagreed about the DR image.
func TestGeneratedReleaseManifestLoadsInApplication(t *testing.T) {
	command := exec.Command("python3", "-c", `
import sys
sys.path.insert(0, "scripts/tests")
from release_manifest_test import ReleaseManifestTest, GENERATOR
fixture = ReleaseManifestTest()
fixture.setUp()
try:
    sys.stdout.buffer.write(GENERATOR.encode(GENERATOR.build(fixture.args())))
finally:
    fixture.tearDown()
`)
	command.Dir = filepath.Join("..", "..")
	payload, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generate manifest: %v\n%s", err, payload)
	}
	path := filepath.Join(t.TempDir(), "release-manifest.json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, projection, err := Load(path, "1.2.0")
	if err != nil {
		t.Fatalf("application rejected generated release manifest: %v", err)
	}
	if len(manifest.Astronomer.Images) != 7 || projection.AgentImage == "" {
		t.Fatalf("incomplete generated manifest/projection: images=%d, agent=%q", len(manifest.Astronomer.Images), projection.AgentImage)
	}
}
