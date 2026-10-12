//go:build unix

package distribution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// This local QA key fixture supplies the original OCI signature config that a
// real Cosign graph contains. Signature payloads/annotations remain unchanged;
// no upstream signature, production OIDC, DNS or actual database semantics are
// claimed by this miniature content fixture.
func newOfflineDatabaseUpdateFixture(t *testing.T, now time.Time) databaseRefreshFixture {
	t.Helper()
	fixture := newDatabaseRefreshFixture(t, now)
	fixture.registry.mutex.Lock()
	defer fixture.registry.mutex.Unlock()
	for name, value := range fixture.registry.manifests {
		if !strings.HasSuffix(name, ".sig") {
			continue
		}
		var manifest ocispec.Manifest
		if decodeClosed(value, &manifest) != nil {
			t.Fatal("fixture signature manifest unavailable")
		}
		manifest.Config = ocispec.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: digest.FromString("{}"), Size: 2, Data: []byte("{}")}
		encoded, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		fixture.registry.manifests[name] = encoded
	}
	fixture.registry.blobs[digest.FromString("{}").String()] = []byte("{}")
	return fixture
}

func exportOfflineDatabaseFixture(t *testing.T) (databaseRefreshFixture, string, time.Time) {
	t.Helper()
	now := time.Now().UTC()
	fixture := newOfflineDatabaseUpdateFixture(t, now)
	root := supplierTransferPrivateDirectory(t)
	cache := supplierTransferPrivateDirectory(t)
	output := filepath.Join(root, "complete-kit")
	result, err := fixture.authority.ExportOfflineUpdateKit(t.Context(), fixture.proof, "1.1.1.1/updates", fixture.feed, cache, output, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Generation <= 0 || result.FeedDigest != fixture.feed || result.MetadataBlobCount == 0 || fixture.main.count() != 1 || fixture.java.count() != 1 {
		t.Fatal("export did not complete both exact signed payloads")
	}
	return fixture, output, now
}

func TestDatabaseOfflineExportAuthenticatesOriginalsAndReadsFinishedKitWithoutNetwork(t *testing.T) {
	fixture, output, now := exportOfflineDatabaseFixture(t)
	index, err := os.ReadFile(filepath.Join(output, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var kit OfflineDatabaseUpdateKit
	if decodeClosed(index, &kit) != nil || len(kit.Subjects) != 3 {
		t.Fatal("closed finished index unavailable")
	}
	fixture.registry.mutex.Lock()
	beforeReads := fixture.registry.payloadReads
	for _, subject := range kit.Subjects {
		repository := strings.TrimPrefix(subject.Repository, "1.1.1.1/")
		original := fixture.registry.manifests[repository+"/"+subject.Digest]
		signature := fixture.registry.manifests[repository+"/sha256-"+strings.TrimPrefix(subject.Digest, "sha256:")+".sig"]
		exported, readErr := os.ReadFile(filepath.Join(output, strings.TrimPrefix(subject.Digest, "sha256:")))
		exportedSignature, signatureErr := os.ReadFile(filepath.Join(output, strings.TrimPrefix(subject.SignatureDigest, "sha256:")))
		if readErr != nil || signatureErr != nil || !bytes.Equal(original, exported) || !bytes.Equal(signature, exportedSignature) {
			fixture.registry.mutex.Unlock()
			t.Fatal("export rewrote original vendor manifest or signature bytes")
		}
	}
	fixture.registry.mutex.Unlock()
	captured, err := fixture.authority.CaptureOfflineUpdateKit(t.Context(), fixture.proof, output, now)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if err := captured.ValidateCurrent(t.Context(), fixture.authority, fixture.proof, now); err != nil {
		t.Fatal(err)
	}
	feed, document, main, java, valid := captured.Snapshot()
	if !valid || feed != fixture.feed || len(document.Databases) != 2 || main.Layer != kit.Main || java.Layer != kit.Java {
		t.Fatal("offline canonical identity differs")
	}
	fixture.registry.mutex.Lock()
	afterReads := fixture.registry.payloadReads
	uploads := fixture.registry.uploadCount
	fixture.registry.mutex.Unlock()
	if afterReads != beforeReads || uploads != 0 {
		t.Fatal("closed import verifier contacted network or published")
	}
}

func TestDatabaseOfflineKitRejectsMissingChangedUnsignedAndUnsafeMembers(t *testing.T) {
	for _, scenario := range []string{"missing-main", "changed-java", "missing-child-signature", "extra-member", "index-unknown", "index-duplicate", "metadata-symlink", "root-public", "rotated-key", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			fixture, output, now := exportOfflineDatabaseFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			indexPath := filepath.Join(output, "index.json")
			index, err := os.ReadFile(indexPath)
			if err != nil {
				t.Fatal(err)
			}
			var kit OfflineDatabaseUpdateKit
			if decodeClosed(index, &kit) != nil {
				t.Fatal("fixture index unavailable")
			}
			switch scenario {
			case "missing-main":
				err = os.Remove(filepath.Join(output, "main.tar.gz"))
			case "changed-java":
				err = os.WriteFile(filepath.Join(output, "java.tar.gz"), []byte("changed complete layer"), 0600)
			case "missing-child-signature":
				err = os.Remove(filepath.Join(output, strings.TrimPrefix(kit.Subjects[1].SignatureDigest, "sha256:")))
			case "extra-member":
				err = os.WriteFile(filepath.Join(output, "unclaimed"), []byte("unclaimed"), 0600)
			case "index-unknown":
				err = os.WriteFile(indexPath, append([]byte(`{"approval":true,`), index[1:]...), 0600)
			case "index-duplicate":
				err = os.WriteFile(indexPath, append([]byte(`{"schema_version":1,`), index[1:]...), 0600)
			case "metadata-symlink":
				path := filepath.Join(output, strings.TrimPrefix(kit.Metadata[0].Digest, "sha256:"))
				replacement := filepath.Join(filepath.Dir(output), "linked-metadata")
				value, readErr := os.ReadFile(path)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if err = os.WriteFile(replacement, value, 0600); err == nil {
					err = os.Remove(path)
				}
				if err == nil {
					err = os.Symlink(replacement, path)
				}
			case "root-public":
				err = os.Chmod(output, 0755)
			case "rotated-key":
				err = os.WriteFile(fixture.trust, []byte("changed current public key"), 0600)
			case "canceled":
				cancel()
			}
			if err != nil {
				t.Fatal(err)
			}
			fixture.registry.mutex.Lock()
			before := fixture.registry.payloadReads
			uploads := fixture.registry.uploadCount
			fixture.registry.mutex.Unlock()
			if _, err := fixture.authority.CaptureOfflineUpdateKit(ctx, fixture.proof, output, now); err == nil {
				t.Fatal("invalid kit accepted")
			}
			fixture.registry.mutex.Lock()
			after := fixture.registry.payloadReads
			afterUploads := fixture.registry.uploadCount
			fixture.registry.mutex.Unlock()
			if before != after || uploads != afterUploads {
				t.Fatal("invalid offline kit fetched or mutated registry")
			}
		})
	}
}

func TestDatabaseOfflineCaptureRejectsChangedOwnerOrPayloadAfterAuthentication(t *testing.T) {
	fixture, output, now := exportOfflineDatabaseFixture(t)
	captured, err := fixture.authority.CaptureOfflineUpdateKit(t.Context(), fixture.proof, output, now)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	otherProof, err := fixture.authority.Base(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := captured.ValidateCurrent(t.Context(), fixture.authority, otherProof, now); err == nil {
		t.Fatal("foreign opaque capture owner accepted")
	}
	if err := os.WriteFile(filepath.Join(output, "main.tar.gz"), []byte("changed after authentication"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := captured.ValidateCurrent(t.Context(), fixture.authority, fixture.proof, now); err == nil {
		t.Fatal("changed pinned payload remained current")
	}
	captured.Close()
	if err := captured.ValidateCurrent(t.Context(), fixture.authority, fixture.proof, now); err == nil {
		t.Fatal("closed capture remained usable")
	}
}

func TestDatabaseOfflineExportRefusesExistingOutputAndCanceledContext(t *testing.T) {
	now := time.Now().UTC()
	fixture := newOfflineDatabaseUpdateFixture(t, now)
	root := supplierTransferPrivateDirectory(t)
	cache := supplierTransferPrivateDirectory(t)
	output := filepath.Join(root, "existing")
	if err := os.WriteFile(output, []byte("operator-owned"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.authority.ExportOfflineUpdateKit(t.Context(), fixture.proof, "1.1.1.1/updates", fixture.feed, cache, output, now); err == nil {
		t.Fatal("existing output replaced")
	}
	value, err := os.ReadFile(output)
	if err != nil || string(value) != "operator-owned" {
		t.Fatal("operator output changed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := fixture.authority.ExportOfflineUpdateKit(ctx, fixture.proof, "1.1.1.1/updates", fixture.feed, cache, filepath.Join(root, "new"), now); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation identity lost")
	}
	if _, err := os.Lstat(filepath.Join(root, "new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled export created output")
	}
}
