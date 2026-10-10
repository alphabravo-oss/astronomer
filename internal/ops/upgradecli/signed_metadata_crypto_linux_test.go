//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	protorekor "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	sigstorebundle "github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	virtualca "github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/tlog"
)

// These fixtures use the concrete production constructor and verifier with an
// independently created local test root. A synthetic certificate carrying the
// exact publisher string proves policy matching, not GitHub-issued provenance.
// No bool, decoder-only seam or production verifier injection admits metadata.
func TestSignedMetadataActualCryptographyReturnsFreshCanonicalManifest(t *testing.T) {
	fixture := newMetadataCryptoFixture(t, "v1.2.2")
	fixture.write(t, "release-manifest.sigstore.json", fixture.sign(t, fixture.manifest, fixture.publisher, metadataIssuer))
	metadata, err := fixture.construct(context.Background())
	if err != nil {
		t.Fatal("actual local keyless metadata verification refused", err)
	}
	t.Cleanup(metadata.Close)
	verified, err := metadata.Verify(context.Background())
	if err != nil || verified.Release.Version != "v1.2.2" || len(verified.Astronomer.Images) != 7 {
		t.Fatal("actual authenticated canonical manifest differs", err)
	}
	verified.Release.Version = "v9.9.9"
	verified.Astronomer.Images[0].Reference = "changed returned copy"
	again, err := metadata.Verify(context.Background())
	if err != nil || again.Release.Version != "v1.2.2" || again.Astronomer.Images[0].Reference == "changed returned copy" {
		t.Fatal("caller-mutated manifest became authority", err)
	}
	metadata.Close()
	if _, err := metadata.Verify(context.Background()); err == nil {
		t.Fatal("closed owned trust admitted verification")
	}
	for _, name := range []string{"release-manifest.json", "release-manifest.sigstore.json"} {
		if _, err := os.Stat(filepath.Join(fixture.directory, name)); err != nil {
			t.Fatal("closing trust removed original release metadata", err)
		}
	}
}

func TestSignedMetadataActualCryptographyRejectsForeignAuthorityAndContent(t *testing.T) {
	for _, change := range []string{"publisher", "issuer", "content", "bundle", "root", "authenticated-schema"} {
		t.Run(change, func(t *testing.T) {
			fixture := newMetadataCryptoFixture(t, "v1.2.2")
			payload, publisher, issuer := fixture.manifest, fixture.publisher, metadataIssuer
			switch change {
			case "publisher":
				publisher = "https://github.com/foreign/repository/.github/workflows/release.yaml@refs/tags/v1.2.2"
			case "issuer":
				issuer = "https://issuer.example.test"
			case "content":
				payload = []byte("different bytes signed by the correct local publisher")
			case "authenticated-schema":
				payload = append([]byte(`{"unknown":true,`), fixture.manifest[1:]...)
				fixture.write(t, "release-manifest.json", payload)
			}
			bundle := fixture.sign(t, payload, publisher, issuer)
			if change == "bundle" {
				bundle = []byte(`{"unsupported":true}`)
			}
			fixture.write(t, "release-manifest.sigstore.json", bundle)
			if change == "root" {
				foreign := newMetadataCryptoFixture(t, "v1.2.2")
				fixture.writeRoot(t, foreign.root)
			}
			if metadata, err := fixture.construct(context.Background()); err == nil {
				metadata.Close()
				t.Fatal("foreign signer, root or content admitted by actual constructor")
			}
		})
	}
}

func TestSignedMetadataActualCryptographyRevalidatesOriginalInputsAndCancellation(t *testing.T) {
	for _, change := range []string{"manifest", "bundle", "mounted-root", "replaced-name", "canceled"} {
		t.Run(change, func(t *testing.T) {
			fixture := newMetadataCryptoFixture(t, "v1.2.2")
			fixture.write(t, "release-manifest.sigstore.json", fixture.sign(t, fixture.manifest, fixture.publisher, metadataIssuer))
			metadata, err := fixture.construct(context.Background())
			if err != nil {
				t.Fatal("positive actual constructor control failed", err)
			}
			t.Cleanup(metadata.Close)
			ctx := context.Background()
			switch change {
			case "manifest":
				fixture.write(t, "release-manifest.json", []byte("changed original manifest"))
			case "bundle":
				fixture.write(t, "release-manifest.sigstore.json", []byte("changed original bundle"))
			case "mounted-root":
				fixture.writeRoot(t, newMetadataCryptoFixture(t, "v1.2.2").root)
			case "replaced-name":
				path := filepath.Join(fixture.directory, "release-manifest.json")
				if err := os.Rename(path, path+".retained"); err != nil {
					t.Fatal(err)
				}
				fixture.write(t, "release-manifest.json", fixture.manifest)
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err = metadata.Verify(ctx)
			if err == nil || (change == "canceled" && !errors.Is(err, context.Canceled)) {
				t.Fatal("changed original or canceled verification admitted", err)
			}
		})
	}
}

func TestSignedRecoveryActualCryptographyRequiresBothSignaturesAndExactPresentArtifacts(t *testing.T) {
	for _, change := range []string{"valid", "foreign-proof-publisher", "wrong-source", "present-artifact-digest"} {
		t.Run(change, func(t *testing.T) {
			fixture := newMetadataCryptoFixture(t, recoveryTag)
			fixture.write(t, "release-manifest.sigstore.json", fixture.sign(t, fixture.manifest, recoveryPublisher, metadataIssuer))
			schema := uint32(1)
			manifestHash := sha256.Sum256(fixture.manifest)
			present := []byte("exact private synthetic runtime evidence")
			presentHash := sha256.Sum256(present)
			record := recoveryRecord{SchemaVersion: &schema, Tag: recoveryTag, SourceCommit: recoverySourceCommit, SourceRunID: recoverySourceRun, ProducerRunID: "123", ProducerCommit: strings.Repeat("b", 40), Files: map[string]string{"release-manifest.json": "sha256:" + hex.EncodeToString(manifestHash[:]), "runtime-image-evidence.json": "sha256:" + hex.EncodeToString(presentHash[:]), "runtime-image-evidence.tar.gz": "sha256:" + strings.Repeat("d", 64), "astronomer-1.2.0.tgz": "sha256:" + strings.Repeat("e", 64)}}
			publisher := recoveryPublisher
			switch change {
			case "foreign-proof-publisher":
				publisher = fixture.publisher
			case "wrong-source":
				record.SourceCommit = strings.Repeat("f", 40)
			case "present-artifact-digest":
				record.Files["runtime-image-evidence.json"] = "sha256:" + strings.Repeat("a", 64)
			}
			raw, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			fixture.write(t, "release-recovery.json", raw)
			fixture.write(t, "release-recovery.sigstore.json", fixture.sign(t, raw, publisher, metadataIssuer))
			fixture.write(t, "runtime-image-evidence.json", present)
			metadata, err := fixture.construct(context.Background())
			if change != "valid" {
				if err == nil {
					metadata.Close()
					t.Fatal("foreign proof or recovery artifact admitted")
				}
				return
			}
			if err != nil {
				t.Fatal("actual dual-signature recovery control refused", err)
			}
			t.Cleanup(metadata.Close)
			verified, err := metadata.Verify(context.Background())
			if err != nil || verified.Release.SourceCommit != recoverySourceCommit {
				t.Fatal("exact authenticated recovery source differs", err)
			}
			fixture.write(t, "runtime-image-evidence.tar.gz", []byte("not present at original capture"))
			if _, err := metadata.Verify(context.Background()); err == nil {
				t.Fatal("new optional artifact inherited earlier acknowledgment")
			}
		})
	}
}

type metadataCryptoFixture struct {
	directory, trustDirectory, target, publisher string
	manifest, root                               []byte
	virtual                                      *virtualca.VirtualSigstore
}

func newMetadataCryptoFixture(t *testing.T, target string) metadataCryptoFixture {
	t.Helper()
	virtual, err := virtualca.NewVirtualSigstore()
	if err != nil {
		t.Fatal(err)
	}
	manifest, _ := canonicalHelmManifestFixture(target)
	if target == recoveryTag {
		manifest.Release.SourceCommit = recoverySourceCommit
	}
	if _, err := manifest.Validate(target); err != nil {
		t.Fatal("canonical manifest fixture is invalid before signing", err)
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	fixture := metadataCryptoFixture{directory: t.TempDir(), trustDirectory: t.TempDir(), target: target, publisher: manifest.Release.SigningPolicy.CertificateIdentity, manifest: raw, virtual: virtual}
	for _, directory := range []string{fixture.directory, fixture.trustDirectory} {
		if err := os.Chmod(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	fixture.root = metadataVirtualRoot(t, virtual)
	fixture.writeRoot(t, fixture.root)
	fixture.write(t, "release-manifest.json", raw)
	return fixture
}

func (f metadataCryptoFixture) construct(ctx context.Context) (*SignedReleaseMetadata, error) {
	return NewSignedReleaseMetadata(ctx, f.directory, f.trustDirectory, f.target, ArtifactPolicy{MaximumBytes: 1 << 20})
}

func (f metadataCryptoFixture) write(t *testing.T, name string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.directory, name), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func (f metadataCryptoFixture) writeRoot(t *testing.T, raw []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.trustDirectory, "trusted_root.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

// This assembly follows the existing canonical resolver keyless fixture. It
// supplies a real message signature, independently signed Rekor SET, certificate
// and mounted test root, rather than an asserted successful verifier result.
func (f metadataCryptoFixture) sign(t *testing.T, artifact []byte, identity, issuer string) []byte {
	t.Helper()
	entity, err := f.virtual.Sign(identity, issuer, artifact)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := entity.VerificationContent()
	if err != nil || certificate.Certificate() == nil {
		t.Fatal("local certificate unavailable", err)
	}
	signature, err := entity.SignatureContent()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := entity.TlogEntries()
	if err != nil || len(entries) != 1 {
		t.Fatal("local transparency entry differs", err)
	}
	entry := entries[0]
	body, ok := entry.Body().(string)
	if !ok || body == "" {
		t.Fatal("local transparency body differs")
	}
	canonicalBody, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		t.Fatal(err)
	}
	var header struct {
		Kind       string `json:"kind"`
		APIVersion string `json:"apiVersion"`
	}
	if err := json.Unmarshal(canonicalBody, &header); err != nil || header.Kind == "" || header.APIVersion == "" {
		t.Fatal("local transparency schema differs", err)
	}
	set, err := f.virtual.RekorSignPayload(tlog.RekorPayload{Body: body, IntegratedTime: entry.IntegratedTime().Unix(), LogIndex: entry.LogIndex(), LogID: hex.EncodeToString([]byte(entry.LogKeyID()))})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(artifact)
	bundle := &protobundle.Bundle{MediaType: "application/vnd.dev.sigstore.bundle+json;version=0.1", VerificationMaterial: &protobundle.VerificationMaterial{Content: &protobundle.VerificationMaterial_X509CertificateChain{X509CertificateChain: &protocommon.X509CertificateChain{Certificates: []*protocommon.X509Certificate{{RawBytes: certificate.Certificate().Raw}}}}, TlogEntries: []*protorekor.TransparencyLogEntry{{LogIndex: entry.LogIndex(), LogId: &protocommon.LogId{KeyId: []byte(entry.LogKeyID())}, KindVersion: &protorekor.KindVersion{Kind: header.Kind, Version: header.APIVersion}, IntegratedTime: entry.IntegratedTime().Unix(), InclusionPromise: &protorekor.InclusionPromise{SignedEntryTimestamp: set}, CanonicalizedBody: canonicalBody}}}, Content: &protobundle.Bundle_MessageSignature{MessageSignature: &protocommon.MessageSignature{MessageDigest: &protocommon.HashOutput{Algorithm: protocommon.HashAlgorithm_SHA2_256, Digest: digest[:]}, Signature: signature.MessageSignatureContent().Signature()}}}
	encoded, err := sigstorebundle.NewBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := encoded.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func metadataVirtualRoot(t *testing.T, virtual *virtualca.VirtualSigstore) []byte {
	t.Helper()
	rekorLogs := virtual.RekorLogs()
	ctLogs := virtual.CTLogs()
	for _, logs := range []map[string]*root.TransparencyLog{rekorLogs, ctLogs} {
		for id, log := range logs {
			raw, err := hex.DecodeString(id)
			if err != nil {
				t.Fatal(err)
			}
			log.ID = raw
		}
	}
	trusted, err := root.NewTrustedRoot(root.TrustedRootMediaType01, virtual.FulcioCertificateAuthorities(), ctLogs, virtual.TimestampingAuthorities(), rekorLogs)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := trusted.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
