package deploy

import (
	"bytes"
	"os"
	"testing"
)

func TestTransitiveRuntimeQualificationIsPromotionBound(t *testing.T) {
	script, err := os.ReadFile("../scripts/qualify-release-runtime-images.py")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{[]byte(`manifest["astronomer"]["runtime_images"]`), []byte(`manifest["flux"]["controllers"]`), []byte(`manifest["built_in_bundles"]["components"]`), []byte(`"linux/amd64","linux/arm64"`), []byte(`"HIGH,CRITICAL"`), []byte(`"spdx-json"`), []byte(`waiver["reference"]`), []byte(`expiry <= now`)} {
		if !bytes.Contains(script, marker) {
			t.Errorf("runtime qualifier missing %q", marker)
		}
	}
	workflow, err := os.ReadFile("../.github/workflows/release.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{[]byte("Qualify every transitive runtime image"), []byte("runtime-image-evidence.sigstore.json"), []byte("--runtime-image-evidence"), []byte("runtime-image-evidence.tar.gz")} {
		if !bytes.Contains(workflow, marker) {
			t.Errorf("release workflow missing runtime gate %q", marker)
		}
	}
}

func TestRCRehearsalHasStrictOwnedDestructionAndRestoreProof(t *testing.T) {
	script, err := os.ReadFile("../scripts/rehearse-release-candidate.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{[]byte(`cluster="astronomer-rc-${run_id}"`), []byte(`RC_DISPOSABLE_ACK`), []byte(`refusing pre-existing cluster`), []byte(`created=0`), []byte(`if ((created))`), []byte(`RC_DECRYPT_PROOF_WEBHOOK_ID`), []byte(`rc-webhook-sink.py`), []byte(`validate-rc-rehearsal-inner.py`), []byte(`previous_chart_digest`), []byte(`BACKUP_SHA256SUMS`), []byte(`scripts/upgrade-release.sh --yes`), []byte(`refusing to overwrite RC evidence`)} {
		if !bytes.Contains(script, marker) {
			t.Errorf("RC rehearsal missing fence/proof %q", marker)
		}
	}
	for _, forbidden := range [][]byte{[]byte(`k3d cluster delete --all`), []byte(`kubectl config use-context`), []byte(`rm -rf "$HOME"`)} {
		if bytes.Contains(script, forbidden) {
			t.Errorf("RC rehearsal contains unsafe operation %q", forbidden)
		}
	}
	workflow, err := os.ReadFile("../.github/workflows/release-candidate-rehearsal.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{[]byte("environment: release-candidate"), []byte("previous_release:"), []byte("source_run_id:"), []byte("Sign sanitized RC evidence"), []byte("Retain sanitized RC evidence only")} {
		if !bytes.Contains(workflow, marker) {
			t.Errorf("RC workflow missing %q", marker)
		}
	}
}
