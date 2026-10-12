//go:build unix

package resolver

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
)

func mountedTrustKey(t *testing.T) (string, []byte) {
	t.Helper()
	_, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	material, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "qa.pub"), material, 0600); err != nil {
		t.Fatal(err)
	}
	return directory, material
}

func TestCosignTrustSnapshotRejectsRotationAndOwnedChanges(t *testing.T) {
	for _, mutation := range []string{"mounted-key", "owned-key", "missing-mounted", "symlink-mounted", "closed"} {
		t.Run(mutation, func(t *testing.T) {
			directory, original := mountedTrustKey(t)
			policy := model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"}
			snapshot, err := CaptureCosignTrust(t.Context(), directory, policy)
			if err != nil {
				t.Fatal(err)
			}
			defer snapshot.Close()
			if snapshot.FrozenDirectory() == directory || snapshot.Fingerprint() != cosignTrustFingerprint(policy, original) {
				t.Fatal("physical snapshot identity differs")
			}
			if err := snapshot.Revalidate(t.Context()); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "qa.pub")
			switch mutation {
			case "mounted-key", "owned-key":
				_, replacement := mountedTrustKey(t)
				if bytes.Equal(replacement, original) {
					t.Fatal("rotation fixture reused key")
				}
				if mutation == "owned-key" {
					path = filepath.Join(snapshot.FrozenDirectory(), "qa.pub")
					if err := os.Chmod(path, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(path, replacement, 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-mounted":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "symlink-mounted":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(snapshot.FrozenDirectory(), "qa.pub"), path); err != nil {
					t.Fatal(err)
				}
			case "closed":
				snapshot.Close()
				snapshot.Close()
			}
			if err := snapshot.Revalidate(t.Context()); err == nil {
				t.Fatal("changed physical trust remained authoritative")
			}
		})
	}
}

// Virtual Sigstore produces actual offline certificate/tlog crypto evidence;
// it is a distinct fixture identity, never production GitHub OIDC qualification.
func TestCosignTrustSnapshotUsesActualOfflineVirtualOIDCRoot(t *testing.T) {
	payload := []byte("virtual trusted-root payload")
	identity, issuer := "qa@example.test", "https://issuer.example.test"
	fixture := newKeylessFixture(t, payload, identity, issuer)
	policy := model.TrustPolicy{Provider: model.SignatureCosignKeyless, Identity: identity, Issuer: issuer}
	snapshot, err := CaptureCosignTrust(t.Context(), fixture.trustDir, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	verifier, err := NewExecVerifier(snapshot.FrozenDirectory())
	if err != nil {
		t.Fatal(err)
	}
	input := VerificationInput{Source: model.Source{Type: model.SourceOCIArtifact, Trust: policy}, Evidence: []SignatureEvidence{{Payload: payload, Bundle: fixture.protoBundle}}}
	verified, err := verifier.Verify(t.Context(), input)
	if err != nil || verified.Status != "verified" {
		t.Fatal("frozen virtual root failed actual offline verification")
	}
	input.Source.Trust.Identity = "wrong@example.test"
	if _, err := verifier.Verify(t.Context(), input); err == nil {
		t.Fatal("wrong identity passed frozen root")
	}
	if err := snapshot.Revalidate(t.Context()); err != nil {
		t.Fatal(err)
	}
	rotated := newKeylessFixture(t, payload, identity, issuer)
	replacement, err := os.ReadFile(filepath.Join(rotated.trustDir, "trusted_root.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.trustDir, "trusted_root.json"), replacement, 0600); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Revalidate(t.Context()); err == nil {
		t.Fatal("old virtual root ignored mounted root rotation")
	}
}

func TestCosignTrustSnapshotBoundsCancellationPolicyAndCleanup(t *testing.T) {
	directory, material := mountedTrustKey(t)
	policy := model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := CaptureCosignTrust(ctx, directory, policy); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture lost cancellation")
	}
	for _, candidate := range []model.TrustPolicy{{AllowUnsigned: true}, {Provider: model.SignatureGit, KeyRef: "qa"}, {Provider: model.SignatureCosignKey, KeyRef: "../qa"}} {
		if snapshot, err := CaptureCosignTrust(t.Context(), directory, candidate); err == nil || snapshot != nil {
			t.Fatal("unsupported policy granted snapshot")
		}
	}
	for _, invalid := range [][]byte{[]byte("invalid root or key"), []byte(strings.Repeat("x", maxTrustKeyBytes+1))} {
		if err := os.WriteFile(filepath.Join(directory, "qa.pub"), invalid, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := CaptureCosignTrust(t.Context(), directory, policy); err == nil {
			t.Fatal("invalid/oversized key captured")
		}
	}
	if err := os.WriteFile(filepath.Join(directory, "qa.pub"), material, 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := CaptureCosignTrust(t.Context(), directory, policy)
	if err != nil {
		t.Fatal(err)
	}
	owned := snapshot.FrozenDirectory()
	if err := snapshot.Revalidate(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled revalidation lost cancellation")
	}
	snapshot.Close()
	if snapshot.Fingerprint() != "" || snapshot.FrozenDirectory() != "" {
		t.Fatal("closed snapshot still selectable")
	}
	if _, err := os.Lstat(owned); !os.IsNotExist(err) {
		t.Fatal("owned trust not cleaned")
	}
	var empty CosignTrustSnapshot
	if err := empty.Revalidate(t.Context()); err == nil {
		t.Fatal("zero snapshot granted authority")
	}
	if cosignTrustFingerprint(policy, material) == cosignTrustFingerprint(model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "another"}, material) {
		t.Fatal("physical fingerprint omitted exact policy")
	}
}

func TestCosignTrustSnapshotCleanupPreservesReplacedOwnedPath(t *testing.T) {
	directory, _ := mountedTrustKey(t)
	snapshot, err := CaptureCosignTrust(t.Context(), directory, model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"})
	if err != nil {
		t.Fatal(err)
	}
	owned := snapshot.FrozenDirectory()
	if err := os.Rename(owned, owned+".original"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(owned); _ = os.RemoveAll(owned + ".original") })
	if err := os.Mkdir(owned, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(owned, "replacement")
	if err := os.WriteFile(marker, []byte("belongs to replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot.Close()
	if _, err := os.Lstat(marker); err != nil {
		t.Fatal("snapshot cleanup removed replaced path")
	}
}
