//go:build unix

package distribution

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
)

// This fixture authenticates the original feed and every child with the same
// canonical keyed consumer. Its DB bytes are opaque QA data, not a real Trivy
// DB, selected Cosign/OIDC publication, a PG acceptance or measured scan use.
func databaseCacheCanonicalFixture(t *testing.T) (*AuthenticatedDatabaseUpdate, map[string]string, time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	registry, document, options, _ := databaseUpdateRegistryFixture(t, now)
	paths := map[string]string{}
	for index := range document.Databases {
		snapshot := &document.Databases[index]
		metadata := databasePayloadMetadata(t, *snapshot)
		payload := encodeDatabasePayloadArchive(t, databasePayloadMembers(*snapshot, metadata), nil)
		repository := "databases/" + snapshot.Role
		subject := registry.fixture.addArtifact(repository, snapshot.Layer.MediaType, payload)
		registry.fixture.mutex.Lock()
		manifestBytes := bytes.Clone(registry.fixture.manifests[repository+"/"+subject])
		registry.fixture.mutex.Unlock()
		var manifest ocispec.Manifest
		if json.Unmarshal(manifestBytes, &manifest) != nil {
			t.Fatal("QA manifest invalid")
		}
		snapshot.Manifest = DatabaseDescriptor{Digest: subject, Size: int64(len(manifestBytes)), MediaType: manifest.MediaType}
		snapshot.Layer.Digest, snapshot.Layer.Size = contentDigest(payload), int64(len(payload))
		snapshot.Metadata.Digest, snapshot.Metadata.Size = contentDigest(metadata), int64(len(metadata))
		snapshot.SupplierSubject = "ghcr.io/aquasecurity/" + snapshot.Role + "@" + subject
		snapshot.PublishedSubject = "1.1.1.1/" + repository + "@" + subject
		paths[snapshot.Role] = databasePayloadOwnedFile(t, payload)
	}
	signer, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil || os.WriteFile(filepath.Join(options.TrustDirectory, "qa.pub"), public, 0600) != nil {
		t.Fatal("QA public key preparation failed")
	}
	options = signDatabaseUpdateFixture(t, registry, document, options, signer)
	loader, err := NewDatabaseUpdateLoader(options)
	if err != nil {
		t.Fatal(err)
	}
	update, err := loader.Load(t.Context(), now)
	if err != nil {
		t.Fatal(err)
	}
	if registry.fixture.externalCount() != 0 {
		t.Fatal("canonical cache fixture contacted external trust")
	}
	return update, paths, now
}

func TestCanonicalSignedFeedMaterializesMeasuredReadOnlyCache(t *testing.T) {
	update, paths, now := databaseCacheCanonicalFixture(t)
	cache, err := MaterializeDatabaseCache(t.Context(), update, paths["trivy-database"], paths["trivy-java-database"], databaseCachePrivateRoot(t), now)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	actual, err := cache.Measure(t.Context())
	if err != nil || actual.FeedDigest != update.digest || actual.Main.Digest != contentDigest([]byte("opaque QA database, not a supplier DB")) || actual.Java.Digest != actual.Main.Digest {
		t.Fatal("canonical signature and actual readonly cache measurements did not compose")
	}
	directory, valid := cache.Directory()
	if !valid {
		t.Fatal("authenticated cache directory unavailable")
	}
	for _, name := range []string{"db/trivy.db", "db/metadata.json", "java-db/trivy-java.db", "java-db/metadata.json"} {
		info, err := os.Lstat(filepath.Join(directory, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0444 {
			t.Fatal("canonical cache member not sealed readonly")
		}
	}
	// This DATA is deliberately not a successful job or scan-used proof.
	if actual.Main.Role != "trivy-database" || actual.Java.Role != "trivy-java-database" {
		t.Fatal("canonical cache roles changed")
	}
}
