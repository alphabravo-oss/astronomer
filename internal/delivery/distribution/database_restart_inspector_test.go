//go:build unix

package distribution

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
)

func databaseRestartFixture(t *testing.T) (DatabaseRestartInspectionOptions, DatabaseAuthoritySnapshot, *registryFixture) {
	t.Helper()
	options, registry, fixture, _ := pinnedDatabaseAuthorityFixture(t)
	authority, err := NewPinnedDatabaseAuthority(t.Context(), options, registry)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := authority.Base(t.Context())
	if err != nil {
		authority.Close()
		t.Fatal(err)
	}
	snapshot, valid := proof.Snapshot()
	authority.Close()
	if !valid {
		t.Fatal("authenticated retained fixture snapshot missing")
	}
	plan := options.MirrorPlan
	path := filepath.Join(t.TempDir(), "mirror.json")
	if err := os.WriteFile(path, plan.verified.payload, 0600); err != nil {
		t.Fatal(err)
	}
	options.MirrorPlan, options.Client = nil, nil
	options.Network = registry.Network
	return DatabaseRestartInspectionOptions{Distribution: options, Registry: &registry, Mirror: &DatabaseRestartMirrorReference{PayloadPath: path, TrustDirectory: plan.verified.trustDirectory, KeyRef: plan.verified.policy.KeyRef}}, snapshot, fixture
}

func TestDatabaseRestartObservesExactLossAfterOriginalConstructorClosed(t *testing.T) {
	for _, role := range []string{"vendor", "operator", "registry"} {
		t.Run(role, func(t *testing.T) {
			options, snapshot, fixture := databaseRestartFixture(t)
			path := filepath.Join(options.Distribution.TrustDirectory, options.Distribution.Policy.KeyRef+".pub")
			switch role {
			case "operator":
				path = filepath.Join(options.Mirror.TrustDirectory, options.Mirror.KeyRef+".pub")
			case "registry":
				path = filepath.Join(options.Registry.TrustDirectory, options.Registry.RootsRef+".pem")
			}
			original, err := os.ReadFile(path) // Owned public material only.
			if err != nil {
				t.Fatal(err)
			}
			defer clear(original)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TMPDIR", filepath.Join(filepath.Dir(path), "absent-temp-parent"))
			before := fixture.readCount()
			inspector, err := NewDatabaseRestartInspector(options)
			if err != nil {
				t.Fatal("restart inspection required lost material or snapshot allocation")
			}
			loss, err := inspector.ObserveLoss(t.Context(), snapshot)
			if err != nil || loss == nil {
				t.Fatal("actual missing retained material did not establish reduce-only observation")
			}
			if err := inspector.RevalidateLoss(t.Context(), loss); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			if err := inspector.RevalidateLoss(t.Context(), loss); !errors.Is(err, ErrNoConfirmedDatabaseRestartTrustLoss) {
				t.Fatal("restored exact material retained old loss authority")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			other, err := NewDatabaseRestartInspector(options)
			if err != nil {
				t.Fatal(err)
			}
			if err := other.RevalidateLoss(t.Context(), loss); err == nil {
				t.Fatal("foreign constructor reused observed restart loss")
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := inspector.ObserveLoss(ctx, snapshot); !errors.Is(err, context.Canceled) {
				t.Fatal("restart observation lost canonical cancellation")
			}
			loss.expires = time.Now().UTC().Add(-time.Second)
			if err := inspector.RevalidateLoss(t.Context(), loss); err == nil {
				t.Fatal("expired restart observation reused")
			}
			if fixture.readCount() != before {
				t.Fatal("restart inspector requested registry content")
			}
		})
	}
}

func TestDatabaseRestartSourceAndConfigurationErrorsCannotBecomeTrustLoss(t *testing.T) {
	options, snapshot, fixture := databaseRestartFixture(t)
	before := fixture.readCount()
	vendor := filepath.Join(options.Distribution.TrustDirectory, options.Distribution.Policy.KeyRef+".pub")
	if err := os.Remove(vendor); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*DatabaseRestartInspectionOptions){
		"vendor-policy": func(o *DatabaseRestartInspectionOptions) {
			o.Distribution.Policy = model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "unconfigured"}
		},
		"vendor-origin":    func(o *DatabaseRestartInspectionOptions) { o.Distribution.TrustDirectory += "/different" },
		"operator-policy":  func(o *DatabaseRestartInspectionOptions) { o.Mirror.KeyRef = "unconfigured" },
		"operator-origin":  func(o *DatabaseRestartInspectionOptions) { o.Mirror.TrustDirectory += "/different" },
		"metadata-missing": func(o *DatabaseRestartInspectionOptions) { o.Mirror.PayloadPath += ".missing" },
		"registry-origin":  func(o *DatabaseRestartInspectionOptions) { o.Registry.TrustDirectory += "/different" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := options
			mirror, registry := *options.Mirror, *options.Registry
			candidate.Mirror, candidate.Registry = &mirror, &registry
			mutate(&candidate)
			inspector, err := NewDatabaseRestartInspector(candidate)
			if err != nil {
				t.Fatal("bounded changed constructor fixture was not established")
			}
			if loss, err := inspector.ObserveLoss(t.Context(), snapshot); err == nil || loss != nil {
				t.Fatal("source/config mismatch granted loss despite unrelated real missing vendor")
			}
		})
	}
	inspector, err := NewDatabaseRestartInspector(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(options.Mirror.PayloadPath, []byte("changed mirror metadata"), 0600); err != nil {
		t.Fatal(err)
	}
	if loss, err := inspector.ObserveLoss(t.Context(), snapshot); err == nil || loss != nil {
		t.Fatal("changed original mirror source granted physical key-loss authority")
	}
	if fixture.readCount() != before {
		t.Fatal("configuration failures refetched registry")
	}
}

func TestDatabaseRestartUnknownMountedErrorAndRetainedMutationFailClosed(t *testing.T) {
	options, snapshot, fixture := databaseRestartFixture(t)
	inspector, err := NewDatabaseRestartInspector(options)
	if err != nil {
		t.Fatal(err)
	}
	if loss, err := inspector.ObserveLoss(t.Context(), snapshot); !errors.Is(err, ErrNoConfirmedDatabaseRestartTrustLoss) || loss != nil {
		t.Fatal("healthy mount allowed a loss observation")
	}
	for name, mutate := range map[string]func(*DatabaseAuthoritySnapshot){
		"scanner": func(s *DatabaseAuthoritySnapshot) {
			s.Base.Binding.Scanner.ImageDigest = "sha256:" + strings.Repeat("e", 64)
		},
		"candidate": func(s *DatabaseAuthoritySnapshot) { s.Base.Binding.CandidateSourceCommit = strings.Repeat("e", 40) },
		"base": func(s *DatabaseAuthoritySnapshot) {
			s.Base.Binding.BaseDistributionDigest = "sha256:" + strings.Repeat("e", 64)
		},
		"catalog": func(s *DatabaseAuthoritySnapshot) { s.Base.Binding.CatalogDigest = "sha256:" + strings.Repeat("e", 64) },
		"vendor-origin": func(s *DatabaseAuthoritySnapshot) {
			s.VendorTrustOriginFingerprint = "sha256:" + strings.Repeat("e", 64)
		},
		"operator-policy": func(s *DatabaseAuthoritySnapshot) { s.MirrorPolicyFingerprint = "sha256:" + strings.Repeat("e", 64) },
		"registry-policy": func(s *DatabaseAuthoritySnapshot) { s.RegistryPolicyFingerprint = "sha256:" + strings.Repeat("e", 64) },
		"physical-frame":  func(s *DatabaseAuthoritySnapshot) { s.TrustMaterialFingerprint = "sha256:" + strings.Repeat("e", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := snapshot
			mutate(&changed)
			if loss, err := inspector.ObserveLoss(t.Context(), changed); err == nil || loss != nil {
				t.Fatal("changed retained data granted observation")
			}
		})
	}
	before := fixture.readCount()
	// ELOOP at the same configured origin is an unknown filesystem failure,
	// not absence or invalid material. No permission bypass fixture is needed.
	directory := options.Distribution.TrustDirectory
	backup := directory + ".owned-original"
	if err := os.Rename(directory, backup); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(directory); _ = os.Rename(backup, directory) })
	if err := os.Symlink(directory, directory); err != nil {
		t.Fatal(err)
	}
	if loss, err := inspector.ObserveLoss(t.Context(), snapshot); err == nil || loss != nil || errors.Is(err, ErrNoConfirmedDatabaseRestartTrustLoss) {
		t.Fatal("unknown mount failure became a loss proof or healthy status")
	}
	if fixture.readCount() != before {
		t.Fatal("unknown filesystem error requested registry")
	}
}

func TestDatabaseRestartChangedValidKeyIsLossWithoutRegistryOrTemporaryTrust(t *testing.T) {
	options, snapshot, fixture := databaseRestartFixture(t)
	inspector, err := NewDatabaseRestartInspector(options)
	if err != nil {
		t.Fatal(err)
	}
	before := fixture.readCount()
	_, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(options.Mirror.TrustDirectory, options.Mirror.KeyRef+".pub")
	if err := os.WriteFile(path, replacement, 0600); err != nil {
		t.Fatal(err)
	}
	loss, err := inspector.ObserveLoss(t.Context(), snapshot)
	if err != nil || loss == nil {
		t.Fatal("different valid operator key was not actual retained-material loss")
	}
	if err := inspector.RevalidateLoss(t.Context(), loss); err != nil {
		t.Fatal(err)
	}
	if fixture.readCount() != before {
		t.Fatal("operator rotation requested registry")
	}
}

func TestDatabaseRestartUnmirroredAuthorityPreservesOriginalPhysicalFraming(t *testing.T) {
	options, fixture := databaseBaseFixture(t)
	authority, err := NewDatabaseAuthority(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := authority.Base(t.Context())
	if err != nil {
		authority.Close()
		t.Fatal(err)
	}
	snapshot, valid := proof.Snapshot()
	authority.Close()
	if !valid || snapshot.TrustMaterialFingerprint != snapshot.VendorTrustFingerprint || snapshot.MirrorTrustFingerprint != "" || snapshot.RegistryRootsFingerprint != "" {
		t.Fatal("unmirrored actual constructor framing changed")
	}
	options.Client = nil
	inspector, err := NewDatabaseRestartInspector(DatabaseRestartInspectionOptions{Distribution: options})
	if err != nil {
		t.Fatal(err)
	}
	before := fixture.readCount()
	if loss, err := inspector.ObserveLoss(t.Context(), snapshot); loss != nil || !errors.Is(err, ErrNoConfirmedDatabaseRestartTrustLoss) {
		t.Fatal("healthy unmirrored material granted loss")
	}
	if err := os.Remove(filepath.Join(options.TrustDirectory, options.Policy.KeyRef+".pub")); err != nil {
		t.Fatal(err)
	}
	if loss, err := inspector.ObserveLoss(t.Context(), snapshot); loss == nil || err != nil {
		t.Fatal("unmirrored removed material required transport or mirror")
	}
	if fixture.readCount() != before {
		t.Fatal("unmirrored restart requested registry")
	}
}

func TestDatabaseRestartConstructorCannotSelectFallbackOrCallerAuthority(t *testing.T) {
	options, _, fixture := databaseRestartFixture(t)
	before := fixture.readCount()
	for name, mutate := range map[string]func(*DatabaseRestartInspectionOptions){
		"binding-missing":   func(o *DatabaseRestartInspectionOptions) { o.Distribution.UpdateBinding = nil },
		"development":       func(o *DatabaseRestartInspectionOptions) { o.Distribution.Development = true },
		"caller-client":     func(o *DatabaseRestartInspectionOptions) { o.Distribution.Client = &http.Client{} },
		"caller-plan":       func(o *DatabaseRestartInspectionOptions) { o.Distribution.MirrorPlan = &MirrorPlan{} },
		"unsigned":          func(o *DatabaseRestartInspectionOptions) { o.Distribution.Policy.AllowUnsigned = true },
		"different-release": func(o *DatabaseRestartInspectionOptions) { o.Distribution.Release = "v9.9.9" },
		"different-parent":  func(o *DatabaseRestartInspectionOptions) { o.Distribution.Digest = "sha256:" + strings.Repeat("e", 64) },
		"different-catalog": func(o *DatabaseRestartInspectionOptions) {
			o.Distribution.CatalogDigest = "sha256:" + strings.Repeat("e", 64)
		},
		"different-profile":         func(o *DatabaseRestartInspectionOptions) { o.Distribution.Profile = "caller" },
		"registry-missing":          func(o *DatabaseRestartInspectionOptions) { o.Registry = nil },
		"registry-policy-disagrees": func(o *DatabaseRestartInspectionOptions) { o.Distribution.Network.Restricted = false },
		"mirror-missing":            func(o *DatabaseRestartInspectionOptions) { o.Mirror = nil },
		"relative-metadata":         func(o *DatabaseRestartInspectionOptions) { o.Mirror.PayloadPath = "relative.json" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := options
			binding, mirror, registry := *options.Distribution.UpdateBinding, *options.Mirror, *options.Registry
			candidate.Distribution.UpdateBinding = &binding
			candidate.Mirror = &mirror
			candidate.Registry = &registry
			mutate(&candidate)
			if inspector, err := NewDatabaseRestartInspector(candidate); err == nil || inspector != nil {
				t.Fatal("unconfigured source/network/trust selected authority")
			}
		})
	}
	if fixture.readCount() != before {
		t.Fatal("constructor guard requested registry")
	}
	var empty DatabaseRestartInspector
	if _, err := empty.ObserveLoss(nil, DatabaseAuthoritySnapshot{}); err == nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
		t.Fatal("empty inspection/context granted loss")
	}
	if err := empty.RevalidateLoss(t.Context(), &ObservedDatabaseRestartTrustLoss{}); err == nil {
		t.Fatal("zero loss reused")
	}
}
