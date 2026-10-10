package distribution

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
)

// This actual keyed registry proves the same base consumer boundary. Its
// archive's scanner is intentionally disabled, so it grants no refresh/scan
// authority or production OIDC identity.
func databaseBaseFixture(t *testing.T) (Options, *registryFixture) {
	t.Helper()
	signer, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	trust := t.TempDir()
	if err := os.WriteFile(filepath.Join(trust, "qa.pub"), public, 0600); err != nil {
		t.Fatal(err)
	}
	fixture := &registryFixture{manifests: map[string][]byte{}, blobs: map[string][]byte{}}
	archive, release, catalogDigest, _ := descriptorFixture(t, fixture, signer, "valid")
	parent := fixture.addArtifact("distribution", MediaType, archive)
	fixture.sign(t, "distribution", parent, signer)
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	client := &http.Client{Transport: registryRoutingTransport{fixture: fixture, target: strings.TrimPrefix(server.URL, "http://"), base: server.Client().Transport}}
	t.Cleanup(client.CloseIdleConnections)
	catalog, err := builtinbundles.Load()
	if err != nil {
		t.Fatal(err)
	}
	_, values, err := ScannerValues(catalog.Components[0].Values)
	if err != nil {
		t.Fatal(err)
	}
	version, image, ok := strings.Cut(values["image"].(map[string]any)["tag"].(string), "@")
	if !ok {
		t.Fatal("fixture scanner identity unavailable")
	}
	binding := DatabaseUpdateBinding{BaseRelease: release, BaseDistributionDigest: parent, CatalogDigest: catalogDigest, Profile: "builtins", CandidateSourceCommit: strings.Repeat("a", 40), Scanner: DatabaseScannerCompatibility{ImageDigest: image, Version: version, MainSchema: 2, JavaSchema: 1}}
	return Options{Repository: "1.1.1.1/distribution", Digest: parent, Release: release, CatalogDigest: catalogDigest, Profile: "builtins", Policy: model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"}, TrustDirectory: trust, Client: client, UpdateBinding: &binding}, fixture
}

func TestDatabaseBaseRequiresActualVerifiedCatalogAndFrozenIntent(t *testing.T) {
	options, _ := databaseBaseFixture(t)
	expected := *options.UpdateBinding
	loader, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	// Caller changes after construction cannot replace frozen candidate intent.
	options.UpdateBinding.CandidateSourceCommit = strings.Repeat("b", 40)
	base, err := loader.DatabaseBase(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, valid := base.Snapshot()
	if !valid || snapshot.Binding != expected || snapshot.PolicyFingerprint != databasePolicyFingerprint(options.Policy) || snapshot.Chart != "trivy-operator" || snapshot.ChartDigest == "" || snapshot.ScanningEnabled {
		t.Fatal("base did not bind actual disabled keyed fixture and constructor")
	}
	// An edited output snapshot is data, never replacement opaque authority.
	snapshot.Binding.CandidateSourceCommit = strings.Repeat("c", 40)
	snapshot.ScanningEnabled = true
	repeated, valid := base.Snapshot()
	if !valid || repeated.Binding != expected || repeated.ScanningEnabled {
		t.Fatal("caller replaced base authority")
	}
	catalog, err := loader.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	catalog.Components[0].Values["trivy"].(map[string]any)["image"] = "caller mutation"
	if _, err := loader.DatabaseBase(t.Context()); err != nil {
		t.Fatal("caller changed verified cached catalog")
	}
}

func TestDatabaseBaseRejectsUnauthenticatedOrDifferentScanner(t *testing.T) {
	for _, mutation := range []string{"unsigned-parent", "wrong-scanner", "missing-binding", "development"} {
		t.Run(mutation, func(t *testing.T) {
			options, fixture := databaseBaseFixture(t)
			switch mutation {
			case "unsigned-parent":
				fixture.mutex.Lock()
				delete(fixture.manifests, "distribution/sha256-"+strings.TrimPrefix(options.Digest, "sha256:")+".sig")
				fixture.mutex.Unlock()
			case "wrong-scanner":
				options.UpdateBinding.Scanner.ImageDigest = contentDigest([]byte("other scanner"))
			case "missing-binding":
				options.UpdateBinding = nil
			case "development":
				options = Options{Development: true}
			}
			loader, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			if base, err := loader.DatabaseBase(t.Context()); err == nil || base != nil {
				t.Fatal("unverified/mismatched base received authority")
			}
		})
	}
	var empty VerifiedDatabaseBase
	if _, valid := empty.Snapshot(); valid {
		t.Fatal("zero base granted authority")
	}
}

func TestDatabaseBaseConstructorRejectsDifferentDistribution(t *testing.T) {
	options, _ := databaseBaseFixture(t)
	expected := *options.UpdateBinding
	for _, field := range []string{"release", "distribution", "catalog", "profile", "candidate", "git-policy"} {
		t.Run(field, func(t *testing.T) {
			candidate := options
			binding := expected
			candidate.UpdateBinding = &binding
			switch field {
			case "release":
				binding.BaseRelease = "v9.9.9"
			case "distribution":
				binding.BaseDistributionDigest = contentDigest([]byte("different"))
			case "catalog":
				binding.CatalogDigest = contentDigest([]byte("different"))
			case "profile":
				binding.Profile = "unqualified"
			case "candidate":
				binding.CandidateSourceCommit = "unknown"
			case "git-policy":
				candidate.Policy.Provider = model.SignatureGit
			}
			if _, err := New(candidate); err == nil {
				t.Fatal("different base constructor accepted")
			}
		})
	}
}

func TestDatabaseBaseCancellationAndPolicyIdentity(t *testing.T) {
	options, _ := databaseBaseFixture(t)
	loader, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := loader.DatabaseBase(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation identity lost")
	}
	first := model.TrustPolicy{Provider: model.SignatureCosignKeyless, Identity: "identity-a", Issuer: "issuer-b"}
	second := model.TrustPolicy{Provider: model.SignatureCosignKeyless, Identity: "identity", Issuer: "aissuer-b"}
	if databasePolicyFingerprint(first) == databasePolicyFingerprint(second) || databasePolicyFingerprint(first) == databasePolicyFingerprint(options.Policy) {
		t.Fatal("different policy fields collided")
	}
}
