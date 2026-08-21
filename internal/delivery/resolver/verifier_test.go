package resolver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	protorekor "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	sigstorebundle "github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	virtualca "github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/tlog"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

func TestExecVerifierVerifiesExactSignedGitCommitAndIdentity(t *testing.T) {
	entity, err := openpgp.NewEntity("Release Bot", "", "release@example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	trustDirectory := t.TempDir()
	var public bytes.Buffer
	armoredKey, err := armor.Encode(&public, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := entity.Serialize(armoredKey); err != nil {
		t.Fatal(err)
	}
	if err := armoredKey.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trustDirectory, "release.asc"), public.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	commit := &object.Commit{
		Author:    object.Signature{Name: "Release Bot", Email: "release@example.test", When: time.Unix(1_700_000_000, 0).UTC()},
		Committer: object.Signature{Name: "Release Bot", Email: "release@example.test", When: time.Unix(1_700_000_000, 0).UTC()},
		TreeHash:  plumbing.NewHash(strings.Repeat("a", 40)), Message: "signed release\n",
	}
	unsigned := &plumbing.MemoryObject{}
	if err := commit.EncodeWithoutSignature(unsigned); err != nil {
		t.Fatal(err)
	}
	reader, err := unsigned.Reader()
	if err != nil {
		t.Fatal(err)
	}
	var signatureBuf bytes.Buffer
	armoredSignature, err := armor.Encode(&signatureBuf, openpgp.SignatureType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := openpgp.DetachSign(armoredSignature, entity, reader, nil); err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	if err := armoredSignature.Close(); err != nil {
		t.Fatal(err)
	}
	commit.PGPSignature = signatureBuf.String()
	encoded := &plumbing.MemoryObject{}
	if err := commit.Encode(encoded); err != nil {
		t.Fatal(err)
	}
	encodedReader, err := encoded.Reader()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(encodedReader)
	if err != nil {
		t.Fatal(err)
	}
	_ = encodedReader.Close()

	verifier := &ExecVerifier{trustDir: trustDirectory}
	fingerprint := strings.ToLower(hex.EncodeToString(entity.PrimaryKey.Fingerprint))
	verified, err := verifier.Verify(context.Background(), VerificationInput{
		Source: model.Source{Type: model.SourceGit, Trust: model.TrustPolicy{
			Provider: model.SignatureGit, KeyRef: "release", Identity: "release@example.test",
		}},
		Revision: model.ImmutableRevision{Kind: model.RevisionGitCommit, Value: encoded.Hash().String()},
		Artifact: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if verified.Status != "verified" || verified.Provider != "openpgp" || verified.Identity != fingerprint {
		t.Fatalf("unexpected verification result: %#v", verified)
	}

	input := VerificationInput{
		Source: model.Source{Type: model.SourceGit, Trust: model.TrustPolicy{
			Provider: model.SignatureGit, KeyRef: "release", Identity: "somebody-else@example.test",
		}},
		Revision: model.ImmutableRevision{Kind: model.RevisionGitCommit, Value: encoded.Hash().String()},
		Artifact: payload,
	}
	if _, err := verifier.Verify(context.Background(), input); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("wrong identity error = %v", err)
	}
	input.Revision.Value = strings.Repeat("b", 40)
	if _, err := verifier.Verify(context.Background(), input); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("wrong object identity error = %v", err)
	}
}

func TestNewExecVerifierStartsWithoutCosignBinary(t *testing.T) {
	trustDirectory := t.TempDir()
	if _, err := os.Stat("/usr/local/bin/cosign"); err == nil {
		t.Log("host has a cosign binary; startup must still succeed without consulting it")
	}
	verifier, err := NewExecVerifier(trustDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if verifier.trustDir != trustDirectory {
		t.Fatalf("trustDir = %q, want %q", verifier.trustDir, trustDirectory)
	}
	if _, err := NewExecVerifier("relative/trust"); err == nil {
		t.Fatal("relative trust directory was accepted")
	}
}

func TestExecVerifierVerifiesCosignKeyOffline(t *testing.T) {
	payload := []byte(`{"critical":{"type":"cosign container image signature","image":{"Docker-manifest-digest":"sha256:cccc"}}}`)
	signer, privateKey, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	rawSignature, err := signer.SignMessage(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	pubPEM, err := cryptoutils.MarshalPublicKeyToPEM(privateKey.Public())
	if err != nil {
		t.Fatal(err)
	}
	trustDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(trustDirectory, "release.pub"), pubPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	verifier, err := NewExecVerifier(trustDirectory)
	if err != nil {
		t.Fatal(err)
	}

	encodedSignature := []byte(base64.StdEncoding.EncodeToString(rawSignature))
	verified, err := verifier.Verify(context.Background(), VerificationInput{
		Source:   model.Source{Type: model.SourceOCIArtifact, Trust: model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "release"}},
		Evidence: []SignatureEvidence{{Payload: payload, Signature: encodedSignature}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if verified.Identity != "key:release" || verified.Provider != "cosign" || verified.Status != "verified" {
		t.Fatalf("unexpected verification result: %#v", verified)
	}

	helmVerified, err := verifier.Verify(context.Background(), VerificationInput{
		Source:    model.Source{Type: model.SourceHelmHTTP, Trust: model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "release"}},
		Artifact:  payload,
		Signature: rawSignature,
	})
	if err != nil {
		t.Fatal(err)
	}
	if helmVerified.Identity != "key:release" {
		t.Fatalf("unexpected helm verification result: %#v", helmVerified)
	}

	tampered := append([]byte(nil), payload...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := verifier.Verify(context.Background(), VerificationInput{
		Source:   model.Source{Type: model.SourceOCIArtifact, Trust: model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "release"}},
		Evidence: []SignatureEvidence{{Payload: tampered, Signature: encodedSignature}},
	}); err == nil {
		t.Fatal("tampered payload was accepted")
	}

	_, otherKey, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	otherPub, err := cryptoutils.MarshalPublicKeyToPEM(otherKey.Public())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trustDirectory, "other.pub"), otherPub, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(context.Background(), VerificationInput{
		Source:   model.Source{Type: model.SourceOCIArtifact, Trust: model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "other"}},
		Evidence: []SignatureEvidence{{Payload: payload, Signature: encodedSignature}},
	}); err == nil {
		t.Fatal("wrong key was accepted")
	}
}

func TestExecVerifierKeylessRequiresOfflineBundle(t *testing.T) {
	trustDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(trustDirectory, "trusted_root.json"), []byte(`{"mediaType":"application/vnd.dev.sigstore.trustedroot+json;version=0.1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	verifier, err := NewExecVerifier(trustDirectory)
	if err != nil {
		t.Fatal(err)
	}
	_, err = verifier.Verify(context.Background(), VerificationInput{
		Source: model.Source{Type: model.SourceOCIArtifact, Trust: model.TrustPolicy{
			Provider: model.SignatureCosignKeyless, Identity: "release@example.test", Issuer: "https://issuer.example.test",
		}},
		Evidence: []SignatureEvidence{{Payload: []byte("payload"), Signature: []byte("signature"), Certificate: []byte("certificate")}},
	})
	if err == nil || !strings.Contains(err.Error(), "verification") {
		t.Fatalf("missing offline Rekor bundle was accepted: %v", err)
	}
}

func TestExecVerifierKeylessRequiresTrustedRoot(t *testing.T) {
	verifier, err := NewExecVerifier(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = verifier.Verify(context.Background(), VerificationInput{
		Source: model.Source{Type: model.SourceOCIArtifact, Trust: model.TrustPolicy{
			Provider: model.SignatureCosignKeyless, Identity: "release@example.test", Issuer: "https://issuer.example.test",
		}},
		Evidence: []SignatureEvidence{{
			Payload:     []byte("payload"),
			Signature:   []byte("signature"),
			Certificate: []byte("certificate"),
			Bundle:      []byte(`{"SignedEntryTimestamp":"QQ==","Payload":{"body":"QQ==","integratedTime":1,"logIndex":1,"logID":"ab"}}`),
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "verification") {
		t.Fatalf("keyless verification without trusted_root.json was accepted: %v", err)
	}
}

func TestExecVerifierKeylessVerifiesOfflineLocalEvidence(t *testing.T) {
	artifact := []byte("air-gapped-release-artifact")
	identity := "release@example.test"
	issuer := "https://issuer.example.test"
	fixture := newKeylessFixture(t, artifact, identity, issuer)

	verifier, err := NewExecVerifier(fixture.trustDir)
	if err != nil {
		t.Fatal(err)
	}

	protoVerified, err := verifier.Verify(context.Background(), VerificationInput{
		Source: model.Source{Type: model.SourceOCIArtifact, Trust: model.TrustPolicy{
			Provider: model.SignatureCosignKeyless, Identity: identity, Issuer: issuer,
		}},
		Evidence: []SignatureEvidence{{Payload: artifact, Bundle: fixture.protoBundle}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if protoVerified.Status != "verified" || protoVerified.Provider != "cosign" || protoVerified.Identity != identity {
		t.Fatalf("unexpected protobuf bundle verification: %#v", protoVerified)
	}

	legacyVerified, err := verifier.Verify(context.Background(), VerificationInput{
		Source: model.Source{Type: model.SourceOCIArtifact, Trust: model.TrustPolicy{
			Provider: model.SignatureCosignKeyless, Identity: identity, Issuer: issuer,
		}},
		Evidence: []SignatureEvidence{{
			Payload:     artifact,
			Signature:   fixture.signature,
			Certificate: fixture.certificate,
			Bundle:      fixture.cosignV2Bundle,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if legacyVerified.Identity != identity {
		t.Fatalf("unexpected Cosign v2 bundle verification: %#v", legacyVerified)
	}

	if _, err := verifier.Verify(context.Background(), VerificationInput{
		Source: model.Source{Type: model.SourceOCIArtifact, Trust: model.TrustPolicy{
			Provider: model.SignatureCosignKeyless, Identity: "other@example.test", Issuer: issuer,
		}},
		Evidence: []SignatureEvidence{{Payload: artifact, Bundle: fixture.protoBundle}},
	}); err == nil {
		t.Fatal("wrong keyless identity was accepted")
	}

	tampered := append([]byte(nil), artifact...)
	tampered[0] ^= 0xff
	if _, err := verifier.Verify(context.Background(), VerificationInput{
		Source: model.Source{Type: model.SourceOCIArtifact, Trust: model.TrustPolicy{
			Provider: model.SignatureCosignKeyless, Identity: identity, Issuer: issuer,
		}},
		Evidence: []SignatureEvidence{{Payload: tampered, Bundle: fixture.protoBundle}},
	}); err == nil {
		t.Fatal("tampered keyless artifact was accepted")
	}
}

func TestExecVerifierDoesNotExposeLibraryOutput(t *testing.T) {
	trustDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(trustDirectory, "release.pub"), []byte("-----BEGIN PUBLIC KEY-----\nnot-a-key\n-----END PUBLIC KEY-----"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := "upstream-secret-must-not-escape"
	verifier, err := NewExecVerifier(trustDirectory)
	if err != nil {
		t.Fatal(err)
	}
	_, err = verifier.Verify(context.Background(), VerificationInput{
		Source:   model.Source{Type: model.SourceOCIArtifact, Trust: model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "release"}},
		Evidence: []SignatureEvidence{{Payload: []byte("payload"), Signature: []byte(secret)}},
	})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("verification error leaked signature material: %v", err)
	}
}

type keylessFixture struct {
	trustDir       string
	certificate    []byte
	signature      []byte
	protoBundle    []byte
	cosignV2Bundle []byte
}

func newKeylessFixture(t *testing.T, artifact []byte, identity, issuer string) keylessFixture {
	t.Helper()
	virtual, err := virtualca.NewVirtualSigstore()
	if err != nil {
		t.Fatal(err)
	}
	entity, err := virtual.Sign(identity, issuer, artifact)
	if err != nil {
		t.Fatal(err)
	}
	verificationContent, err := entity.VerificationContent()
	if err != nil || verificationContent.Certificate() == nil {
		t.Fatalf("leaf certificate: %v", err)
	}
	certificatePEM, err := cryptoutils.MarshalCertificateToPEM(verificationContent.Certificate())
	if err != nil {
		t.Fatal(err)
	}
	signatureContent, err := entity.SignatureContent()
	if err != nil {
		t.Fatal(err)
	}
	rawSignature := signatureContent.MessageSignatureContent().Signature()
	entries, err := entity.TlogEntries()
	if err != nil || len(entries) == 0 {
		t.Fatalf("tlog entries: %v", err)
	}
	entry := entries[0]
	body, ok := entry.Body().(string)
	if !ok || body == "" {
		t.Fatal("tlog body is not canonical hashedrekord JSON")
	}
	payload := tlog.RekorPayload{
		Body:           body,
		IntegratedTime: entry.IntegratedTime().Unix(),
		LogIndex:       entry.LogIndex(),
		LogID:          hex.EncodeToString([]byte(entry.LogKeyID())),
	}
	set, err := virtual.RekorSignPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	canonicalBody, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		t.Fatal(err)
	}
	kind, version := hashedRekordKind(canonicalBody)
	tle := &protorekor.TransparencyLogEntry{
		LogIndex: entry.LogIndex(),
		LogId:    &protocommon.LogId{KeyId: []byte(entry.LogKeyID())},
		KindVersion: &protorekor.KindVersion{
			Kind:    kind,
			Version: version,
		},
		IntegratedTime: entry.IntegratedTime().Unix(),
		InclusionPromise: &protorekor.InclusionPromise{
			SignedEntryTimestamp: set,
		},
		CanonicalizedBody: canonicalBody,
	}
	digest := sha256Sum(artifact)
	pb := &protobundle.Bundle{
		MediaType: cosignV2BundleMediaTyp,
		VerificationMaterial: &protobundle.VerificationMaterial{
			Content: &protobundle.VerificationMaterial_X509CertificateChain{
				X509CertificateChain: &protocommon.X509CertificateChain{
					Certificates: []*protocommon.X509Certificate{{RawBytes: verificationContent.Certificate().Raw}},
				},
			},
			TlogEntries: []*protorekor.TransparencyLogEntry{tle},
		},
		Content: &protobundle.Bundle_MessageSignature{
			MessageSignature: &protocommon.MessageSignature{
				MessageDigest: &protocommon.HashOutput{
					Algorithm: protocommon.HashAlgorithm_SHA2_256,
					Digest:    digest,
				},
				Signature: rawSignature,
			},
		},
	}
	protoEntity, err := sigstorebundle.NewBundle(pb)
	if err != nil {
		t.Fatal(err)
	}
	protoJSON, err := protoEntity.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	legacy := cosignRekorBundle{SignedEntryTimestamp: set}
	legacy.Payload.Body = json.RawMessage([]byte(`"` + body + `"`))
	legacy.Payload.IntegratedTime = payload.IntegratedTime
	legacy.Payload.LogIndex = payload.LogIndex
	legacy.Payload.LogID = payload.LogID
	legacyJSON, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}

	trustDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(trustDirectory, "trusted_root.json"), virtualTrustedRootJSON(t, virtual), 0o600); err != nil {
		t.Fatal(err)
	}
	return keylessFixture{
		trustDir:       trustDirectory,
		certificate:    certificatePEM,
		signature:      []byte(base64.StdEncoding.EncodeToString(rawSignature)),
		protoBundle:    protoJSON,
		cosignV2Bundle: legacyJSON,
	}
}

func virtualTrustedRootJSON(t *testing.T, virtual *virtualca.VirtualSigstore) []byte {
	t.Helper()
	rekorLogs := virtual.RekorLogs()
	for hexID, log := range rekorLogs {
		raw, err := hex.DecodeString(hexID)
		if err != nil {
			t.Fatal(err)
		}
		log.ID = raw
	}
	ctLogs := virtual.CTLogs()
	for hexID, log := range ctLogs {
		raw, err := hex.DecodeString(hexID)
		if err != nil {
			t.Fatal(err)
		}
		log.ID = raw
	}
	trusted, err := root.NewTrustedRoot(root.TrustedRootMediaType01, virtual.FulcioCertificateAuthorities(), ctLogs, virtual.TimestampingAuthorities(), rekorLogs)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := trusted.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func sha256Sum(value []byte) []byte {
	sum := sha256.Sum256(value)
	return sum[:]
}
