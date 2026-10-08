package deploy

import (
	"bytes"
	"os"
	"testing"

	"gopkg.in/yaml.v3"
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

// A deferred external certification must never bypass failed automated qualification.
func TestApprovedV12PromotionRequiresAutomatedRehearsal(t *testing.T) {
	for _, path := range []string{"../.github/workflows/release.yaml", "../.github/workflows/resume-release.yaml"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var workflow struct {
			Jobs map[string]struct {
				If    string    `yaml:"if"`
				Needs yaml.Node `yaml:"needs"`
				Steps []struct {
					Name string            `yaml:"name"`
					If   string            `yaml:"if"`
					Run  string            `yaml:"run"`
					With map[string]string `yaml:"with"`
				} `yaml:"steps"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(raw, &workflow); err != nil {
			t.Fatal(err)
		}
		approved := `contains(fromJSON('["v1.2.0","v1.2.1"]'), needs.preflight.outputs.image-tag)`
		externalGate := "!" + approved
		rc := workflow.Jobs["rc-rehearsal"]
		rcCondition := approved
		if path == "../.github/workflows/resume-release.yaml" {
			rcCondition = "always() && !cancelled() && needs.preflight.result == 'success' && needs.qualify.result == 'success' && " + approved
		}
		if rc.If != rcCondition {
			t.Errorf("%s: exception is not scoped to approved releases", path)
		}
		promote := workflow.Jobs["promote"]
		found := false
		var dependencies []string
		if err := promote.Needs.Decode(&dependencies); err != nil {
			t.Fatal(err)
		}
		for _, need := range dependencies {
			if need == "rc-rehearsal" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: promotion does not wait for rehearsal", path)
		}
		for _, clause := range []string{"!cancelled()", "needs.preflight.result == 'success'", "needs.qualify.result == 'success'", approved + " && needs.rc-rehearsal.result == 'success'", externalGate + " && needs.rc-rehearsal.result == 'skipped'"} {
			if !bytes.Contains([]byte(promote.If), []byte(clause)) {
				t.Errorf("%s: missing fail-closed clause %s", path, clause)
			}
		}
		verified, external, downloaded := false, false, false
		for _, step := range promote.Steps {
			if bytes.Contains([]byte(step.Run), []byte("validate-automated-release.py")) {
				verified = step.If == approved
			}
			if bytes.Contains([]byte(step.Run), []byte("validate-release-approval.py")) {
				external = (step.If == externalGate || step.If == "${{ "+externalGate+" }}")
			}
			if step.With["name"] == "rc-rehearsal-${{ github.run_id }}" {
				downloaded = step.If == approved && step.With["run-id"] == ""
			}
		}
		if !verified || !external || !downloaded {
			t.Errorf("%s: missing scoped verifier, future external gate, or current-run evidence", path)
		}
	}
}
