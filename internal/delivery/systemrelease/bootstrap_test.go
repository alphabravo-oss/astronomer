package systemrelease

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
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
	if first.ArtifactURL != "oci://registry.example.test/astronomer/system" || !strings.HasPrefix(first.Version, "v1.0.0+system.") || first.AgentVersion != "v1.0.0" {
		t.Fatalf("release was not normalized: %#v", first)
	}
}

func TestBuildPreservesPrereleaseWhenAddingSystemIdentityMetadata(t *testing.T) {
	config := validConfig()
	config.Version = "1.2.0-local.52350a38.a60bcdd3"
	config.AgentVersion = config.Version
	spec, _, err := build(config)
	if err != nil {
		t.Fatalf("local prerelease system release rejected: %v", err)
	}
	if !strings.HasPrefix(spec.Version, "v1.2.0-local.52350a38.a60bcdd3+system.") {
		t.Fatalf("system identity did not preserve prerelease version: %q", spec.Version)
	}
	if len(spec.Version) > 64 {
		t.Fatalf("system release version exceeds the database column bound: %d (%q)", len(spec.Version), spec.Version)
	}
}

func TestBuildRejectsPartialConfiguration(t *testing.T) {
	for _, mutate := range []func(*Config){
		func(config *Config) { config.ArtifactDigest = "" },
		func(config *Config) { config.ArtifactRepository = "" },
	} {
		config := validConfig()
		mutate(&config)
		if _, _, err := build(config); err == nil {
			t.Fatal("partial release configuration accepted")
		}
	}
}

func TestBuildSupportsOfflinePinnedCosignKey(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded})
	config := validConfig()
	config.CertificateIssuer = ""
	config.CertificateIdentity = ""
	config.PublicKey = publicKey
	spec, digest, err := build(config)
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" || len(spec.Verification.OIDCIdentities) != 0 || len(spec.Verification.PublicKeys) != 1 ||
		string(spec.Verification.PublicKeys[0]) != string(publicKey) {
		t.Fatalf("offline public-key policy not bound into release: %#v", spec.Verification)
	}

	config.CertificateIssuer = validConfig().CertificateIssuer
	if _, _, err := build(config); err == nil {
		t.Fatal("mixed public-key and keyless trust was accepted")
	}
	config.CertificateIssuer = ""
	config.PublicKey = []byte("not a public key")
	if _, _, err := build(config); err == nil {
		t.Fatal("malformed public key was accepted")
	}
}

func TestBuildSupportsOverlappingOfflineKeyRotation(t *testing.T) {
	makeKey := func() []byte {
		t.Helper()
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
		if err != nil {
			t.Fatal(err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded})
	}
	oldKey, nextKey := makeKey(), makeKey()
	config := validConfig()
	config.CertificateIssuer = ""
	config.CertificateIdentity = ""
	config.PublicKeys = [][]byte{oldKey, nextKey}
	spec, _, err := build(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Verification.PublicKeys) != 2 || len(spec.Verification.OIDCIdentities) != 0 {
		t.Fatalf("rotating keyring not included in immutable release: %#v", spec.Verification)
	}
	oldOnly := validConfig()
	oldOnly.CertificateIssuer = ""
	oldOnly.CertificateIdentity = ""
	oldOnly.PublicKeys = [][]byte{oldKey}
	oldSpec, _, err := build(oldOnly)
	if err != nil {
		t.Fatal(err)
	}
	if oldSpec.Version == spec.Version {
		t.Fatal("changing the trust keyring reused an immutable system release identity")
	}
}

func TestBuildAllowsUnconfiguredDevelopment(t *testing.T) {
	config := validConfig()
	config.ArtifactRepository = ""
	config.ArtifactDigest = ""
	spec, digest, err := build(config)
	if err != nil || digest != "" || spec.Version != "" {
		t.Fatalf("unconfigured development release = %#v %q %v", spec, digest, err)
	}
}
