package deploy

import (
	"bytes"
	"os"
	"testing"
)

func TestCredentialedCloudAcceptanceIsFailSafeAndSanitized(t *testing.T) {
	script, err := os.ReadFile("../scripts/validate-cloud-acceptance.py")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range [][]byte{
		[]byte(`PROVIDERS = {"eks", "gke", "aks", "doks"}`),
		[]byte(`ASTRO_EXPECTED_BASE_URL`), []byte(`globally routable /32 or /128 host`),
		[]byte(`finally:`), []byte(`reversed(targets)`), []byte(`idempotent_replay`),
		[]byte(`credential is not explicitly linked to adopted cluster`),
		[]byte(`sorted(set(current.get("effective", []))) == original["effective"]`),
		[]byte(`".recovery"`), []byte(`os.chmod(output, 0o644)`),
	} {
		if !bytes.Contains(script, required) {
			t.Errorf("cloud acceptance driver missing contract %q", required)
		}
	}
	for _, forbidden := range [][]byte{[]byte("CreateCluster"), []byte("create_cluster"), []byte("secret_access_key"), []byte("client_secret"), []byte("service_account_json")} {
		if bytes.Contains(script, forbidden) {
			t.Errorf("cloud acceptance driver contains provisioning/secret surface %q", forbidden)
		}
	}
	workflow, err := os.ReadFile("../.github/workflows/cloud-acceptance.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range [][]byte{[]byte("environment: cloud-acceptance"), []byte("workflow_dispatch:"), []byte("Sign sanitized evidence"), []byte("ASTRO_CLOUD_ACCEPTANCE_TOKEN")} {
		if !bytes.Contains(workflow, required) {
			t.Errorf("protected workflow missing %q", required)
		}
	}
	if bytes.Contains(workflow, []byte(".recovery/")) {
		t.Fatal("workflow uploads private recovery snapshots")
	}
}
