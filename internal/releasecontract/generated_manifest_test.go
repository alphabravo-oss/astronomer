package releasecontract

import (
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
				manifest.Astronomer.Images[4] = testArtifact(tc.replaceWorker, 'c')
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
