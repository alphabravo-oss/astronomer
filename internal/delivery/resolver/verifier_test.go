package resolver

import (
	"bytes"
	"context"
	"encoding/hex"
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
	var signature bytes.Buffer
	armoredSignature, err := armor.Encode(&signature, openpgp.SignatureType, nil)
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
	commit.PGPSignature = signature.String()
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

func TestExecVerifierCosignUsesOnlyOfflineLocalEvidence(t *testing.T) {
	trustDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(trustDirectory, "release.pub"), []byte("fixture-public-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(t.TempDir(), "capture")
	cosign := filepath.Join(t.TempDir(), "cosign")
	script := "#!/bin/sh\n" +
		"printf '%s\\n%s\\n%s\\n%s\\n%s\\n' \"$*\" \"$HOME\" \"$DOCKER_CONFIG\" \"$HTTPS_PROXY\" \"$HTTP_PROXY\" > " + shellSingleQuote(capture) + "\n"
	if err := os.WriteFile(cosign, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	verifier, err := NewExecVerifier(cosign, trustDirectory)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"critical":{"type":"cosign container image signature","image":{"Docker-manifest-digest":"sha256:cccc"}}}`)
	verified, err := verifier.Verify(context.Background(), VerificationInput{
		Source:   model.Source{Type: model.SourceOCIArtifact, Trust: model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "release"}},
		Evidence: []SignatureEvidence{{Payload: payload, Signature: []byte("base64-signature")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if verified.Identity != "key:release" || verified.Provider != "cosign" {
		t.Fatalf("unexpected verification result: %#v", verified)
	}
	captured, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	text := string(captured)
	if !strings.Contains(text, "verify-blob --offline --signature ") || !strings.Contains(text, " --key "+filepath.Join(trustDirectory, "release.pub")+" ") {
		t.Fatalf("cosign did not receive the offline local-evidence command: %q", text)
	}
	if strings.Contains(text, "http://") || strings.Contains(text, "https://") || strings.Contains(text, "registry") {
		t.Fatalf("cosign received a network destination: %q", text)
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 3 {
		// strings.TrimSpace removes the two deliberately-empty proxy lines.
		t.Fatalf("unexpected verifier environment capture: %q", text)
	}
}

func TestExecVerifierKeylessRequiresOfflineBundle(t *testing.T) {
	trustDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(trustDirectory, "release.pub"), []byte("fixture-public-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	cosign := filepath.Join(t.TempDir(), "cosign")
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(cosign, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	verifier, err := NewExecVerifier(cosign, trustDirectory)
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

func TestExecVerifierDoesNotExposeCosignOutput(t *testing.T) {
	trustDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(trustDirectory, "release.pub"), []byte("fixture-public-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	cosign := filepath.Join(t.TempDir(), "cosign")
	secret := "upstream-secret-must-not-escape"
	if err := os.WriteFile(cosign, []byte("#!/bin/sh\necho "+shellSingleQuote(secret)+" >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	verifier, err := NewExecVerifier(cosign, trustDirectory)
	if err != nil {
		t.Fatal(err)
	}
	_, err = verifier.Verify(context.Background(), VerificationInput{
		Source:   model.Source{Type: model.SourceOCIArtifact, Trust: model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "release"}},
		Evidence: []SignatureEvidence{{Payload: []byte("payload"), Signature: []byte("signature")}},
	})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("verification error leaked subprocess output: %v", err)
	}
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
