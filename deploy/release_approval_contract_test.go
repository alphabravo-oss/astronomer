package deploy

import (
	"bytes"
	"os"
	"testing"
)

func TestPromotionRequiresProtectedDigestBoundExternalApproval(t *testing.T) {
	for _, path := range []string{"../.github/workflows/release.yaml", "../.github/workflows/resume-release.yaml"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, required := range [][]byte{[]byte("environment: release-production"), []byte("RELEASE_APPROVAL_JSON"), []byte("fetch-release-qualification-artifacts.py"), []byte("validate-release-approval.py"), []byte("--rc-bundle"), []byte("--cloud-bundle"), []byte("--scale-bundle"), []byte("--rancher-bundle"), []byte("--accessibility-bundle"), []byte(`kubernetes_minor: "1.33"`), []byte(`kubernetes_minor: "1.34"`), []byte(`kubernetes_minor: "1.35"`), []byte(`--image "$K3S_IMAGE"`)} {
			if !bytes.Contains(raw, required) {
				t.Errorf("%s missing promotion gate %q", path, required)
			}
		}
	}
	schema, err := os.ReadFile("release/release-approval.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range [][]byte{[]byte(`"additionalProperties": false`), []byte(`"evidence_digest"`), []byte(`"source_run_id"`), []byte(`"source_ref"`), []byte(`"artifact_name"`), []byte(`"scale_certification"`), []byte(`"rancher_benchmark"`), []byte(`"accessibility"`), []byte(`"const": "passed"`)} {
		if !bytes.Contains(schema, required) {
			t.Errorf("release approval schema omits %q", required)
		}
	}
}
