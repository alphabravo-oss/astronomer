//go:build unix

package distribution

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
)

func TestDatabasePhysicalAuthorityBindsActualCatalogBeforeCachedRotation(t *testing.T) {
	options, _ := databaseBaseFixture(t)
	counter := &physicalAuthorityRequestCounter{base: options.Client.Transport}
	options.Client.Transport = counter
	authority, err := NewDatabaseAuthority(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()
	proof, err := authority.Base(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, valid := proof.Snapshot()
	if !valid || !digestPattern.MatchString(snapshot.TrustMaterialFingerprint) || snapshot.TrustMaterialFingerprint != authority.trust.Fingerprint() || snapshot.MirrorTrustFingerprint != "" || snapshot.MirrorTrustOriginFingerprint != "" || snapshot.Base.Binding.MirrorEndorsementDigest != "" || snapshot.Base.DistributionIdentity != options.Digest || snapshot.Base.ScanningEnabled {
		t.Fatal("physical base did not preserve actual disabled QA catalog")
	}
	if err := authority.Revalidate(t.Context(), proof); err != nil {
		t.Fatal(err)
	}
	// A second constructor cannot consume an opaque proof from this one.
	other, err := NewDatabaseAuthority(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.Revalidate(t.Context(), proof); err == nil {
		t.Fatal("foreign physical constructor proof accepted")
	}
	before := counter.count.Load()
	if err := os.WriteFile(filepath.Join(options.TrustDirectory, "qa.pub"), []byte("rotated mounted material"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := authority.Revalidate(t.Context(), proof); err == nil {
		t.Fatal("cached daemon ignored mounted rotation")
	}
	if repeated, err := authority.Base(t.Context()); err == nil || repeated != nil {
		t.Fatal("cached catalog renewed physical authority after rotation")
	}
	after := counter.count.Load()
	if after != before {
		t.Fatal("rotation rejection refetched catalog before failing physical trust")
	}
}

func TestDatabasePhysicalAuthorityRejectsUnauthenticatedClosedAndCanceled(t *testing.T) {
	options, fixture := databaseBaseFixture(t)
	authority, err := NewDatabaseAuthority(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()
	fixture.mutex.Lock()
	delete(fixture.manifests, "distribution/sha256-"+options.Digest[len("sha256:"):]+".sig")
	fixture.mutex.Unlock()
	if proof, err := authority.Base(t.Context()); err == nil || proof != nil {
		t.Fatal("physical snapshot promoted unsigned catalog")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := authority.Base(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("physical base lost cancellation")
	}
	authority.Close()
	authority.Close()
	if _, err := authority.Base(t.Context()); err == nil {
		t.Fatal("closed constructor granted authority")
	}
	var zero VerifiedDatabaseAuthorityBase
	if _, valid := zero.Snapshot(); valid {
		t.Fatal("zero proof granted physical authority")
	}
	options.UpdateBinding = nil
	if _, err := NewDatabaseAuthority(t.Context(), options); err == nil {
		t.Fatal("missing binding granted physical authority")
	}
}

// Count all manifest, signature and payload requests, not merely blob reads.
type physicalAuthorityRequestCounter struct {
	base  http.RoundTripper
	count atomic.Int64
}

func (counter *physicalAuthorityRequestCounter) RoundTrip(request *http.Request) (*http.Response, error) {
	counter.count.Add(1)
	return counter.base.RoundTrip(request)
}
func (counter *physicalAuthorityRequestCounter) CloseIdleConnections() {
	if closer, ok := counter.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func TestDatabasePhysicalAuthorityUsesSameFrozenTrustForSignedUpdate(t *testing.T) {
	options, fixture := databaseBaseFixture(t)
	signer, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(options.TrustDirectory, "qa.pub"), public, 0600); err != nil {
		t.Fatal(err)
	}
	// Keep the original immutable catalog/child subjects, re-sign under this
	// local QA key so the feed and canonical catalog share one physical policy.
	fixture.mutex.Lock()
	var subjects []string
	for name := range fixture.manifests {
		if !strings.HasSuffix(name, ".sig") {
			subjects = append(subjects, name)
		}
	}
	fixture.mutex.Unlock()
	for _, name := range subjects {
		repository, subject, ok := strings.Cut(name, "/sha256:")
		if !ok {
			t.Fatal("QA immutable subject missing")
		}
		fixture.sign(t, repository, "sha256:"+subject, signer)
	}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	document := databaseUpdateFixture(now)
	document.Binding = *options.UpdateBinding
	for index := range document.Databases {
		snapshot := &document.Databases[index]
		repository := "updates/" + snapshot.Role
		subject := fixture.addArtifact(repository, snapshot.Layer.MediaType, []byte("opaque QA payload: "+snapshot.Role))
		fixture.mutex.Lock()
		original := bytes.Clone(fixture.manifests[repository+"/"+subject])
		fixture.mutex.Unlock()
		var manifest ocispec.Manifest
		if decodeClosed(original, &manifest) != nil {
			t.Fatal("QA child manifest invalid")
		}
		snapshot.Manifest = DatabaseDescriptor{Digest: subject, Size: int64(len(original)), MediaType: manifest.MediaType}
		snapshot.Config = DatabaseDescriptor{Digest: manifest.Config.Digest.String(), Size: manifest.Config.Size, MediaType: manifest.Config.MediaType}
		snapshot.Layer = DatabaseDescriptor{Digest: manifest.Layers[0].Digest.String(), Size: manifest.Layers[0].Size, MediaType: manifest.Layers[0].MediaType}
		snapshot.SupplierSubject = "ghcr.io/aquasecurity/" + snapshot.Role + "@" + subject
		snapshot.PublishedSubject = "1.1.1.1/" + repository + "@" + subject
		fixture.sign(t, repository, subject, signer)
	}
	digest := fixture.addArtifact("updates", DatabaseUpdateMediaType, encodeDatabaseUpdateFixture(t, document))
	fixture.sign(t, "updates", digest, signer)
	authority, err := NewDatabaseAuthority(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()
	proof, err := authority.Base(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	update, err := authority.LoadUpdate(t.Context(), proof, "1.1.1.1/updates", digest, now)
	if err != nil {
		t.Fatal(err)
	}
	observedDigest, observed, valid := update.Snapshot()
	if !valid || observedDigest != digest || observed.Binding != document.Binding || len(observed.Databases) != 2 {
		t.Fatal("same frozen trust failed canonical signed feed/child binding")
	}
	// Opaque payload strings have never been inspected as actual database files;
	// this positive grants descriptor authentication only, not freshness/use.
	if err := os.WriteFile(filepath.Join(options.TrustDirectory, "qa.pub"), []byte("changed mounted key"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := authority.LoadUpdate(t.Context(), proof, "1.1.1.1/updates", digest, now); err == nil {
		t.Fatal("signed feed accepted through stale frozen trust after rotation")
	}
}
