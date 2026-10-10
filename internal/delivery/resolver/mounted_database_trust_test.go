//go:build unix

package resolver

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

func TestMountedSigningInspectorRetainsKnownOriginWithoutSnapshotAllocation(t *testing.T) {
	directory, original := mountedTrustKey(t)
	_, replacement := mountedTrustKey(t)
	policy := model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"}
	captured, err := CaptureCosignTrust(t.Context(), directory, policy)
	if err != nil {
		t.Fatal(err)
	}
	expected, origin := captured.Fingerprint(), captured.OriginFingerprint()
	captured.Close()
	// A frozen-directory allocator cannot work here. The inspector must still
	// inspect the actual public mount, without requiring a functioning loader.
	t.Setenv("TMPDIR", filepath.Join(directory, "absent-temp-parent"))
	inspector, err := NewMountedCosignTrustInspector(directory, policy)
	if err != nil {
		t.Fatal(err)
	}
	policy.KeyRef = "caller-replacement"
	actual, err := inspector.Fingerprint(t.Context())
	if err != nil || actual != expected || inspector.OriginFingerprint(expected) != origin {
		t.Fatal("no-allocation inspector differs from canonical captured signing trust")
	}
	path := filepath.Join(directory, "qa.pub")
	for _, mutation := range []string{"missing", "empty", "oversized", "invalid", "symlink", "valid-rotation"} {
		t.Run(mutation, func(t *testing.T) {
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "missing":
				err = os.Remove(path)
			case "empty":
				err = os.WriteFile(path, nil, 0600)
			case "oversized":
				err = os.WriteFile(path, bytes.Repeat([]byte("x"), maxTrustKeyBytes+1), 0600)
			case "invalid":
				err = os.WriteFile(path, []byte("not a public key"), 0600)
			case "symlink":
				err = os.Remove(path)
				if err == nil {
					err = os.Symlink(path+".outside", path)
				}
			case "valid-rotation":
				err = os.WriteFile(path, replacement, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			fingerprint, inspectErr := inspector.Fingerprint(t.Context())
			if mutation == "valid-rotation" {
				if inspectErr != nil || fingerprint == expected || fingerprint != cosignTrustFingerprint(inspector.policy, replacement) {
					t.Fatal("valid changed key did not produce its exact different fingerprint")
				}
			} else if !errors.Is(inspectErr, ErrMountedDatabaseTrustLost) || fingerprint != "" {
				t.Fatal("definite missing/invalid material was not classified as loss")
			}
			if inspector.OriginFingerprint(expected) != origin {
				t.Fatal("missing material erased configured historical-origin comparison")
			}
			if mutation == "symlink" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := inspector.Fingerprint(ctx); !errors.Is(err, context.Canceled) || errors.Is(err, ErrMountedDatabaseTrustLost) {
		t.Fatal("cancellation became physical material loss")
	}
	if _, err := inspector.Fingerprint(nil); err == nil || errors.Is(err, ErrMountedDatabaseTrustLost) {
		t.Fatal("missing context became physical material loss")
	}
}

func TestMountedSigningInspectorUsesCanonicalOfflineVirtualOIDCRoot(t *testing.T) {
	// This is an actual virtual Sigstore root/parser, not production OIDC startup.
	fixture := newKeylessFixture(t, []byte("mounted root"), "qa@example.test", "https://issuer.example.test")
	policy := model.TrustPolicy{Provider: model.SignatureCosignKeyless, Identity: "qa@example.test", Issuer: "https://issuer.example.test"}
	captured, err := CaptureCosignTrust(t.Context(), fixture.trustDir, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	inspector, err := NewMountedCosignTrustInspector(fixture.trustDir, policy)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := inspector.Fingerprint(t.Context())
	if err != nil || fingerprint != captured.Fingerprint() || inspector.OriginFingerprint(fingerprint) != captured.OriginFingerprint() {
		t.Fatal("canonical keyless trusted-root interpretation changed")
	}
	if err := os.WriteFile(filepath.Join(fixture.trustDir, "trusted_root.json"), []byte(`{"unexpected":"shape"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := inspector.Fingerprint(t.Context()); !errors.Is(err, ErrMountedDatabaseTrustLost) {
		t.Fatal("invalid trusted root did not produce known public material loss")
	}
}

func TestMountedRegistryInspectorFreezesBindingsAndCannotRequestRegistry(t *testing.T) {
	options, requests, roots := registryTrustFixture(t)
	_, otherRequests, replacement := registryTrustFixture(t)
	captured, err := CaptureDatabaseRegistryTransport(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	expected, policy := captured.Fingerprints()
	origin := captured.RootOriginFingerprint()
	captured.Close()
	inspector, err := NewMountedDatabaseRegistryInspector(options)
	if err != nil {
		t.Fatal(err)
	}
	options.Network.Origins[0].URL = "https://caller-changed.test"
	if observed, observedOrigin := inspector.Bindings(); observed != policy || observedOrigin != origin {
		t.Fatal("inspector did not retain exact constructor transport policy/origin")
	}
	actual, err := inspector.Fingerprint(t.Context())
	if err != nil || actual != expected || actual != registryRootFingerprint(roots) {
		t.Fatal("mounted CA parser/hash differs from actual canonical transport")
	}
	path := filepath.Join(options.TrustDirectory, "registry.pem")
	if err := os.WriteFile(path, replacement, 0600); err != nil {
		t.Fatal(err)
	}
	if actual, err := inspector.Fingerprint(t.Context()); err != nil || actual == expected || actual != registryRootFingerprint(replacement) {
		t.Fatal("valid CA rotation was hidden")
	}
	for _, mutation := range []string{"invalid", "non-ca", "missing"} {
		t.Run(mutation, func(t *testing.T) {
			switch mutation {
			case "invalid":
				err = os.WriteFile(path, []byte("not PEM"), 0600)
			case "non-ca":
				err = os.WriteFile(path, bytes.ReplaceAll(roots, []byte("CERTIFICATE"), []byte("PRIVATE KEY")), 0600)
			case "missing":
				err = os.Remove(path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if fingerprint, err := inspector.Fingerprint(t.Context()); !errors.Is(err, ErrMountedDatabaseTrustLost) || fingerprint != "" {
				t.Fatal("invalid or missing CA material was not definite loss")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := inspector.Fingerprint(ctx); !errors.Is(err, context.Canceled) || errors.Is(err, ErrMountedDatabaseTrustLost) {
		t.Fatal("registry inspection cancellation became loss")
	}
	if requests.Load() != 0 || otherRequests.Load() != 0 {
		t.Fatal("mounted-only inspection made a registry request")
	}
}

func TestMountedMaterialClassificationDoesNotRevokeForPermissionIOOrUnknown(t *testing.T) {
	directory, _ := mountedTrustKey(t)
	info, err := os.Lstat(filepath.Join(directory, "qa.pub"))
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []error{os.ErrPermission, syscall.EIO, syscall.ENOMEM, syscall.EINTR, context.Canceled, context.DeadlineExceeded, errors.New("unknown state")} {
		if knownMountedDatabaseMaterialLoss(nil, failure) || knownMountedDatabaseMaterialLoss(info, failure) {
			t.Fatal("unclassified error granted physical material loss")
		}
	}
	if knownMountedDatabaseMaterialLoss(info, nil) || knownMountedDatabaseMaterialLoss(nil, nil) || !knownMountedDatabaseMaterialLoss(nil, os.ErrNotExist) {
		t.Fatal("mounted stat classification lost definite absence/regular-file distinction")
	}
	for _, policy := range []model.TrustPolicy{{}, {AllowUnsigned: true}, {Provider: model.SignatureCosignKey, KeyRef: "../outside"}} {
		if inspector, err := NewMountedCosignTrustInspector(directory, policy); err == nil || inspector != nil || errors.Is(err, ErrMountedDatabaseTrustLost) {
			t.Fatal("invalid constructor configuration became loss or an inspector")
		}
	}
	var zero MountedCosignTrustInspector
	if _, err := zero.Fingerprint(t.Context()); err == nil || errors.Is(err, ErrMountedDatabaseTrustLost) {
		t.Fatal("unowned signing inspector granted a loss fact")
	}
	var registry MountedDatabaseRegistryInspector
	if _, err := registry.Fingerprint(t.Context()); err == nil || errors.Is(err, ErrMountedDatabaseTrustLost) {
		t.Fatal("unowned registry inspector granted a loss fact")
	}
	options := DatabaseRegistryTrustOptions{TrustDirectory: directory, RootsRef: "../outside", Network: httpclient.DestinationPolicy{Restricted: true, Origins: []httpclient.TrustedOrigin{{URL: "https://registry.example.test", CIDRs: []netip.Prefix{netip.MustParsePrefix("8.8.8.8/32")}}}}}
	if _, err := NewMountedDatabaseRegistryInspector(options); err == nil || errors.Is(err, ErrMountedDatabaseTrustLost) {
		t.Fatal("unapproved registry configuration became physical loss")
	}
	if strings.Contains(ErrMountedDatabaseTrustLost.Error(), directory) {
		t.Fatal("known-loss diagnostic exposed a mounted path")
	}
}
