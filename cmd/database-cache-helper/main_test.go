package main

import (
	"os"
	"testing"
)

func TestHelperInitializationRefusesMixedModesAndInvalidClaims(t *testing.T) {
	for _, test := range []struct {
		name                                        string
		input, coordination, material, incompatible bool
		claim                                       string
	}{
		{name: "no-mode"}, {name: "two-modes", input: true, coordination: true},
		{name: "three-modes", input: true, coordination: true, material: true},
		{name: "missing-claim", input: true}, {name: "invalid-claim", input: true, claim: "other"},
		{name: "nil-claim", input: true, claim: "00000000-0000-0000-0000-000000000000"},
		{name: "coordination-claim", coordination: true, claim: "9824ad66-a4ed-42a5-8101-d9436c7de0e6"},
		{name: "material-claim", material: true, claim: "9824ad66-a4ed-42a5-8101-d9436c7de0e6"},
		{name: "unrelated-option", input: true, incompatible: true, claim: "9824ad66-a4ed-42a5-8101-d9436c7de0e6"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if actual := initializeJobProcess(test.input, test.coordination, test.material, test.claim, test.incompatible); actual != 2 {
				t.Fatalf("invalid arguments reached fixed filesystem adapter: exit %d", actual)
			}
		})
	}
}
func TestHelperInitializationFlagsRefuseScanAndTrustArgumentsBeforeFilesystem(t *testing.T) {
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	for _, args := range [][]string{
		{"--initialize-input", "--claim-id", "invalid"},
		{"--initialize-coordination", "--initialize-material"},
		{"--initialize-material", "--bootstrap-material"},
		{"--initialize-coordination", "--private-pull"},
		{"--initialize-input", "--claim-id", "9824ad66-a4ed-42a5-8101-d9436c7de0e6", "--stage"},
		{"--initialize-material", "--cache-root", "/arbitrary"},
		{"--initialize-coordination", "--feed-digest", "sha256:arbitrary"},
		{"--initialize-coordination", "--workload-repository", "registry.example.test/workload"},
	} {
		os.Args = append([]string{"database-cache-helper"}, args...)
		if code := run(); code != 2 {
			t.Fatalf("mixed init mode reached filesystem or typed configuration: exit %d", code)
		}
	}
}

func TestHelperMappedReleaseFlagsRefuseNonBootstrapRoutesBeforeFilesystem(t *testing.T) {
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	for _, args := range [][]string{
		{"--mapped-release"},
		{"--mapped-release", "--initialize-material"},
		{"--mapped-release", "--initialize-input", "--claim-id", "9824ad66-a4ed-42a5-8101-d9436c7de0e6"},
		{"--mapped-release", "--stage", "--cache-root", "/arbitrary", "--coordination-root", "/arbitrary"},
		{"--mapped-release", "--prepare", "--cache-root", "/arbitrary", "--coordination-root", "/arbitrary"},
		{"--mapped-release", "--await-prepared", "--cache-root", "/arbitrary", "--coordination-root", "/arbitrary"},
		{"--mapped-release", "--cache-root", "/arbitrary", "--coordination-root", "/arbitrary"},
		{"--mapped-release", "--bootstrap-material", "--feed-digest", "sha256:arbitrary"},
		{"--mapped-release", "--bootstrap-material", "--claim-id", "9824ad66-a4ed-42a5-8101-d9436c7de0e6"},
	} {
		os.Args = append([]string{"database-cache-helper"}, args...)
		if code := run(); code != 2 {
			t.Fatalf("mapped material selector reached unrelated filesystem or configuration route: exit %d", code)
		}
	}
}
