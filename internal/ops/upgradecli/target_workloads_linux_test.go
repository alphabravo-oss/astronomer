//go:build linux

package upgradecli

import (
	"strings"
	"testing"
)

func TestTargetWorkloadInventoryRejectsEmptyDuplicateAndForeignRows(t *testing.T) {
	actual, err := targetWorkloadIdentities([]byte("worker\tworker-uid\towned\nserver\tserver-uid\towned\n"), "owned")
	if err != nil || len(actual) != 2 || actual["server"].uid != "server-uid" {
		t.Fatal("exact selected deployment identities rejected")
	}
	for _, raw := range []string{"", "server\tserver-uid\tother\n", "server\t\towned\n", "server\tfirst\towned\nserver\tsecond\towned\n", strings.Repeat("server\tuid\towned\n", 10001)} {
		if _, err := targetWorkloadIdentities([]byte(raw), "owned"); err == nil {
			t.Fatal("incomplete or ambiguous workload inventory admitted")
		}
	}
}

func TestTargetRenderedPinsRetainLegacyRolesAndManagedAgentContract(t *testing.T) {
	manifest, _ := canonicalHelmManifestFixture("v1.2.2")
	var raw strings.Builder
	agent := ""
	for _, image := range manifest.Astronomer.Images {
		if image.Name == "agent" {
			agent = image.Reference
			continue
		}
		if image.Name != "cli" {
			raw.WriteString("image: " + image.Reference + "\n")
		}
	}
	raw.WriteString("AGENT_IMAGE_REPOSITORY: \"" + agent + "\"\nAGENT_IMAGE_TAG: \"v1.2.2\"\n")
	if err := targetManifestPins([]byte(raw.String()), manifest); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"server", "worker", "frontend", "shell"} {
		for _, image := range manifest.Astronomer.Images {
			if image.Name == role {
				bad := strings.Replace(raw.String(), image.Reference, "changed.invalid/image", 1)
				if err := targetManifestPins([]byte(bad), manifest); err == nil {
					t.Fatal("missing image pin admitted")
				}
			}
		}
	}
	for _, bad := range []string{strings.Replace(raw.String(), "AGENT_IMAGE_TAG: \"v1.2.2\"", "AGENT_IMAGE_TAG: \"1.2.2\"", 1), strings.Replace(raw.String(), "AGENT_IMAGE_REPOSITORY", "OTHER_IMAGE_REPOSITORY", 1), "", strings.Repeat("x", (16<<20)+1)} {
		if err := targetManifestPins([]byte(bad), manifest); err == nil {
			t.Fatal("managed agent or finite capture contract bypassed")
		}
	}
}

func TestTargetObservationConstructorRequiresSameReleaseAndDatabaseOwner(t *testing.T) {
	if _, err := NewTargetWorkloads(nil, nil); err == nil {
		t.Fatal("missing composition admitted")
	}
	first := &HelmRelease{}
	second := &HelmRelease{}
	observation := &HelmStateObservation{release: first, source: &SourceDatabaseBackup{}}
	if _, err := NewTargetWorkloads(observation, &ReadyzObservation{release: second}); err == nil {
		t.Fatal("different health owner admitted")
	}
	observation.source = nil
	if _, err := NewTargetWorkloads(observation, &ReadyzObservation{release: first}); err == nil {
		t.Fatal("external database inferred")
	}
}
