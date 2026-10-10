//go:build unix

package distribution

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
)

type databaseRefreshFixture struct {
	authority   *DatabaseAuthority
	proof       *VerifiedDatabaseAuthorityBase
	registry    *registryFixture
	update      *AuthenticatedDatabaseUpdate
	main, java  *supplierTransferTransport
	trust, feed string
}

// The fixture signs an actual parent/feed/child graph using a local QA key.
// Only transport routing and finite Range responses are injected. It grants
// no production DNS, GitHub OIDC, supplier-size or real database semantics.
func newDatabaseRefreshFixture(t *testing.T, now time.Time) databaseRefreshFixture {
	t.Helper()
	options, registry := databaseBaseFixture(t)
	signer, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	trust := filepath.Join(options.TrustDirectory, "qa.pub")
	if err := os.WriteFile(trust, public, 0600); err != nil {
		t.Fatal(err)
	}
	registry.mutex.Lock()
	var subjects []string
	for name := range registry.manifests {
		if !strings.HasSuffix(name, ".sig") {
			subjects = append(subjects, name)
		}
	}
	registry.mutex.Unlock()
	for _, name := range subjects {
		repository, subject, ok := strings.Cut(name, "/sha256:")
		if !ok {
			t.Fatal("fixture immutable subject absent")
		}
		registry.sign(t, repository, "sha256:"+subject, signer)
	}
	document := databaseUpdateFixture(now)
	document.Binding = *options.UpdateBinding
	var transfers [2]*supplierTransferTransport
	for index := range document.Databases {
		snapshot := &document.Databases[index]
		metadata := databasePayloadMetadata(t, *snapshot)
		payload := encodeDatabasePayloadArchive(t, databasePayloadMembers(*snapshot, metadata), nil)
		repository := "updates/" + snapshot.Role
		subject := registry.addArtifact(repository, snapshot.Layer.MediaType, payload)
		registry.mutex.Lock()
		original := bytes.Clone(registry.manifests[repository+"/"+subject])
		registry.mutex.Unlock()
		var manifest ocispec.Manifest
		if decodeClosed(original, &manifest) != nil {
			t.Fatal("fixture original child invalid")
		}
		snapshot.Manifest = DatabaseDescriptor{Digest: subject, Size: int64(len(original)), MediaType: manifest.MediaType}
		snapshot.Config = DatabaseDescriptor{Digest: manifest.Config.Digest.String(), Size: manifest.Config.Size, MediaType: manifest.Config.MediaType}
		snapshot.Layer = DatabaseDescriptor{Digest: manifest.Layers[0].Digest.String(), Size: manifest.Layers[0].Size, MediaType: manifest.Layers[0].MediaType}
		snapshot.Metadata.Digest, snapshot.Metadata.Size = contentDigest(metadata), int64(len(metadata))
		snapshot.SupplierSubject = "ghcr.io/aquasecurity/" + snapshot.Role + "@" + subject
		snapshot.PublishedSubject = "1.1.1.1/" + repository + "@" + subject
		registry.sign(t, repository, subject, signer)
		transfers[index] = &supplierTransferTransport{base: options.Client.Transport, path: "/v2/" + repository + "/blobs/" + snapshot.Layer.Digest, payload: payload}
	}
	transfers[0].base = transfers[1]
	options.Client = &http.Client{Transport: transfers[0], Timeout: time.Minute}
	feed := registry.addArtifact("updates", DatabaseUpdateMediaType, encodeDatabaseUpdateFixture(t, document))
	registry.sign(t, "updates", feed, signer)
	registry.mutex.Lock()
	registry.manifests["updates/current"] = bytes.Clone(registry.manifests["updates/"+feed])
	registry.mutex.Unlock()
	authority, err := NewDatabaseAuthority(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(authority.Close)
	proof, err := authority.Base(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	update, err := authority.DiscoverUpdate(t.Context(), proof, "1.1.1.1/updates", now)
	if err != nil {
		t.Fatal(err)
	}
	return databaseRefreshFixture{authority: authority, proof: proof, registry: registry, update: update, main: transfers[0], java: transfers[1], trust: trust, feed: feed}
}

func TestDatabaseRefreshCapturesSignedWholePayloadsBeforeOwnedCleanup(t *testing.T) {
	now := time.Now().UTC()
	fixture := newDatabaseRefreshFixture(t, now)
	cache := supplierTransferPrivateDirectory(t)
	captured, err := fixture.authority.CaptureRefreshPayloads(t.Context(), fixture.proof, "1.1.1.1/updates", fixture.update, cache, now)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	update, main, java, valid := captured.Snapshot()
	feed, _, _ := update.Snapshot()
	if !valid || feed != fixture.feed || main.Role != "trivy-database" || java.Role != "trivy-java-database" || fixture.main.count() != 1 || fixture.java.count() != 1 {
		t.Fatal("complete signed capture identity differs")
	}
	if err := captured.ValidateCurrent(t.Context(), fixture.authority, fixture.proof, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	captured.Close()
	if err := captured.ValidateCurrent(t.Context(), fixture.authority, fixture.proof, time.Now().UTC()); err == nil {
		t.Fatal("closed capture remained current")
	}
	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) != 0 {
		t.Fatal("owned capture cleanup left staged files")
	}
}

func TestDatabaseRefreshFailsBeforeStagingForUntrustedChangedOrCanceledFeed(t *testing.T) {
	for _, scenario := range []string{"unsigned-child", "changed-feed", "rotated-key", "foreign-proof", "canceled", "private-repository"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC()
			fixture := newDatabaseRefreshFixture(t, now)
			cache := supplierTransferPrivateDirectory(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			repository := "1.1.1.1/updates"
			proof := fixture.proof
			switch scenario {
			case "unsigned-child":
				_, document, _ := fixture.update.Snapshot()
				child, subject, _ := strings.Cut(strings.TrimPrefix(document.Databases[0].PublishedSubject, "1.1.1.1/"), "@")
				fixture.registry.mutex.Lock()
				delete(fixture.registry.manifests, child+"/sha256-"+strings.TrimPrefix(subject, "sha256:")+".sig")
				fixture.registry.mutex.Unlock()
			case "changed-feed":
				fixture.registry.mutex.Lock()
				fixture.registry.manifests["updates/"+fixture.feed] = []byte("changed immutable bytes")
				fixture.registry.mutex.Unlock()
			case "rotated-key":
				if err := os.WriteFile(fixture.trust, []byte("changed key"), 0600); err != nil {
					t.Fatal(err)
				}
			case "foreign-proof":
				proof = &VerifiedDatabaseAuthorityBase{}
			case "canceled":
				cancel()
			case "private-repository":
				repository = "10.0.0.1/updates"
			}
			if _, err := fixture.authority.CaptureRefreshPayloads(ctx, proof, repository, fixture.update, cache, now); err == nil {
				t.Fatal("untrusted refresh entered owned capture")
			}
			if fixture.main.count() != 0 || fixture.java.count() != 0 {
				t.Fatal("failed feed reached layer bytes")
			}
			entries, err := os.ReadDir(cache)
			if err != nil || len(entries) != 0 {
				t.Fatal("failed feed staged payload files")
			}
		})
	}
}

func TestDatabaseRefreshRejectsPartialCaptureAndPreservesReplacementFiles(t *testing.T) {
	now := time.Now().UTC()
	fixture := newDatabaseRefreshFixture(t, now)
	cache := supplierTransferPrivateDirectory(t)
	fixture.java.scenario = "changed"
	if _, err := fixture.authority.CaptureRefreshPayloads(t.Context(), fixture.proof, "1.1.1.1/updates", fixture.update, cache, now); err == nil {
		t.Fatal("partial failed sweep produced capture")
	}
	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) != 0 {
		t.Fatal("partial capture cleanup left bytes")
	}
	fixture.java.scenario = ""
	captured, err := fixture.authority.CaptureRefreshPayloads(t.Context(), fixture.proof, "1.1.1.1/updates", fixture.update, cache, now)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	path := captured.path(0)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement owned elsewhere"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := captured.ValidateCurrent(t.Context(), fixture.authority, fixture.proof, now); err == nil {
		t.Fatal("replacement file remained captured authority")
	}
	captured.Close()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "replacement owned elsewhere" {
		t.Fatal("capture cleanup removed replacement")
	}
}
