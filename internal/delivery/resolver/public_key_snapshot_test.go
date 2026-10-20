package resolver

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestPublicKeySnapshotAuthenticatesCapturedBytesAndRejectsUnsafeInputs(t *testing.T) {
	signer, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	filename := filepath.Join(source, "qa.pub")
	if err := os.WriteFile(filename, public, 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := CaptureMountedPublicKey(source, "qa")
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if snapshot.Directory() == source || snapshot.Reference() != "qa" {
		t.Fatal("snapshot reused mutable source directory")
	}
	if err := os.WriteFile(filename, []byte("changed mounted key"), 0600); err != nil {
		t.Fatal(err)
	}
	changed := snapshot.Material()
	clear(changed)
	if !bytes.Equal(snapshot.Material(), public) {
		t.Fatal("caller mutation changed captured key")
	}
	payload := []byte("owned QA document")
	signed, err := signer.SignMessage(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewExecVerifier(snapshot.Directory())
	if err != nil {
		t.Fatal(err)
	}
	verified, err := verifier.Verify(t.Context(), VerificationInput{Source: model.Source{Type: model.SourceHelmHTTP, Trust: model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"}}, Artifact: payload, Signature: signed})
	if err != nil || verified.Status != "verified" {
		t.Fatal("captured key did not authenticate original signature")
	}
	for _, reference := range []string{"missing", "../qa", "qa/other"} {
		if _, err := CaptureMountedPublicKey(source, reference); err == nil {
			t.Fatal("unsafe key reference accepted")
		}
	}
	for _, data := range [][]byte{[]byte("invalid"), append(bytes.Clone(public), []byte("trailing")...), append([]byte("prefix"), public...), append(bytes.Clone(public), public...), []byte(strings.Repeat("x", protocol.MaxDeliveryCredentialValue+1))} {
		if err := os.WriteFile(filename, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := CaptureMountedPublicKey(source, "qa"); err == nil {
			t.Fatal("invalid/oversized public key accepted")
		}
	}
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(snapshot.Directory(), "qa.pub"), filename); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureMountedPublicKey(source, "qa"); err == nil {
		t.Fatal("symbolic public key accepted")
	}
	directory := snapshot.Directory()
	snapshot.Close()
	snapshot.Close()
	if len(snapshot.Material()) != 0 {
		t.Fatal("closed snapshot retained selectable key")
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("owned public trust directory was not cleaned")
	}
}
