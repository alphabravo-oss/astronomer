package resolver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

func TestOfflineMetadataReusesActualCryptographyWithSelfContainedBundle(t *testing.T) {
	artifact := []byte(`{"schema_version":1,"tag":"v1.2.0"}`)
	identity := "release@example.test"
	issuer := "https://issuer.example.test"
	fixture := newKeylessFixture(t, artifact, identity, issuer)
	verifier, err := NewExecVerifier(fixture.trustDir)
	if err != nil {
		t.Fatal(err)
	}
	policy := model.TrustPolicy{Provider: model.SignatureCosignKeyless, Identity: identity, Issuer: issuer}
	proof, err := verifier.VerifyOfflineMetadata(context.Background(), artifact, fixture.protoBundle, policy)
	if err != nil || proof.Status != "verified" || proof.Identity != identity || proof.Provider != "cosign" {
		t.Fatal("shared cryptographic metadata verification failed", err)
	}
	// This fixture proves the shared verification primitive, not a real
	// Astronomer publisher, the recovery semantic contract or a released artifact.
}

func TestOfflineMetadataRejectsWrongPublisherIssuerContentBundleAndRoot(t *testing.T) {
	for _, change := range []string{"identity", "issuer", "artifact", "bundle", "missing-root", "wrong-root"} {
		t.Run(change, func(t *testing.T) {
			artifact := []byte("exact synthetic metadata")
			identity := "release@example.test"
			issuer := "https://issuer.example.test"
			fixture := newKeylessFixture(t, artifact, identity, issuer)
			verifier, err := NewExecVerifier(fixture.trustDir)
			if err != nil {
				t.Fatal(err)
			}
			policy := model.TrustPolicy{Provider: model.SignatureCosignKeyless, Identity: identity, Issuer: issuer}
			bundle := fixture.protoBundle
			switch change {
			case "identity":
				policy.Identity = "other@example.test"
			case "issuer":
				policy.Issuer = "https://foreign.example.test"
			case "artifact":
				artifact = []byte("tampered synthetic metadata")
			case "bundle":
				bundle = []byte(`{"unsupported":true}`)
			case "missing-root":
				if err := os.Remove(filepath.Join(fixture.trustDir, "trusted_root.json")); err != nil {
					t.Fatal(err)
				}
			case "wrong-root":
				if err := os.WriteFile(filepath.Join(fixture.trustDir, "trusted_root.json"), []byte(`{"mediaType":"unsupported"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := verifier.VerifyOfflineMetadata(context.Background(), artifact, bundle, policy); err == nil {
				t.Fatal("untrusted or altered metadata verified")
			}
		})
	}
}

func TestOfflineMetadataRequiresFiniteExplicitSignedPolicyAndLiveContext(t *testing.T) {
	artifact := []byte("exact synthetic metadata")
	fixture := newKeylessFixture(t, artifact, "release@example.test", "https://issuer.example.test")
	verifier, err := NewExecVerifier(fixture.trustDir)
	if err != nil {
		t.Fatal(err)
	}
	policy := model.TrustPolicy{Provider: model.SignatureCosignKeyless, Identity: "release@example.test", Issuer: "https://issuer.example.test"}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := verifier.VerifyOfflineMetadata(canceled, artifact, fixture.protoBundle, policy); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation identity lost")
	}
	if _, err := verifier.VerifyOfflineMetadata(nil, artifact, fixture.protoBundle, policy); err == nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
		t.Fatal("nil context admitted")
	}
	var missing *ExecVerifier
	if _, err := missing.VerifyOfflineMetadata(context.Background(), artifact, fixture.protoBundle, policy); err == nil {
		t.Fatal("nil verifier admitted")
	}
	for _, change := range []string{"artifact-empty", "bundle-empty", "artifact-limit", "bundle-limit", "unsigned", "key-provider", "no-identity", "no-issuer"} {
		t.Run(change, func(t *testing.T) {
			payload := artifact
			bundle := fixture.protoBundle
			trust := policy
			switch change {
			case "artifact-empty":
				payload = nil
			case "bundle-empty":
				bundle = nil
			case "artifact-limit":
				payload = []byte(strings.Repeat("x", 1<<20+1))
			case "bundle-limit":
				bundle = []byte(strings.Repeat("x", 1<<20+1))
			case "unsigned":
				trust.AllowUnsigned = true
			case "key-provider":
				trust.Provider = model.SignatureCosignKey
			case "no-identity":
				trust.Identity = ""
			case "no-issuer":
				trust.Issuer = ""
			}
			if _, err := verifier.VerifyOfflineMetadata(context.Background(), payload, bundle, trust); err == nil {
				t.Fatal("unbounded or unsigned metadata contract admitted")
			}
		})
	}
}
