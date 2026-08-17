package systemrelease

import (
	"reflect"
	"strings"
	"testing"
)

func validConfig() Config {
	return Config{
		Enabled: true, Version: "1.0.0",
		ArtifactRepository: "registry.example.test/astronomer/system",
		ArtifactDigest:     "sha256:" + strings.Repeat("a", 64),
		DistributionDigest: "sha256:" + strings.Repeat("b", 64),
		AgentVersion:       "1.0.0",
		AgentImage:         "registry.example.test/astronomer/agent@sha256:" + strings.Repeat("c", 64),
		MinimumKubernetes:  "1.33", MaximumKubernetes: "1.35",
		CertificateIssuer:   "https://token.actions.githubusercontent.com",
		CertificateIdentity: "https://github.com/example/astronomer/.github/workflows/release.yaml@refs/tags/v1.0.0",
	}
}

func TestBuildProducesValidatedDeterministicRelease(t *testing.T) {
	first, firstDigest, err := build(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	second, secondDigest, err := build(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest || !reflect.DeepEqual(first, second) {
		t.Fatal("release construction is not deterministic")
	}
	if first.ArtifactURL != "oci://registry.example.test/astronomer/system" || first.Version != "v1.0.0" || first.AgentVersion != "v1.0.0" {
		t.Fatalf("release was not normalized: %#v", first)
	}
}

func TestBuildRejectsPartialConfiguration(t *testing.T) {
	config := validConfig()
	config.ArtifactDigest = ""
	if _, _, err := build(config); err == nil {
		t.Fatal("partial release configuration accepted")
	}
}

func TestBuildAllowsUnconfiguredDevelopment(t *testing.T) {
	spec, digest, err := build(Config{Enabled: true})
	if err != nil || digest != "" || spec.Version != "" {
		t.Fatalf("unconfigured development release = %#v %q %v", spec, digest, err)
	}
}
