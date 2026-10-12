//go:build unix

package resolver

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestMountedPublicMetadataOnlyHashesExactBoundedRegularInput(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "mirror.json")
	payload := []byte("original public authenticated metadata bytes")
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	observed, err := FingerprintMountedPublicMetadata(t.Context(), path)
	expected := fmt.Sprintf("sha256:%x", sha256.Sum256(payload))
	if err != nil || observed != expected {
		t.Fatal("bounded regular metadata did not preserve original byte identity")
	}
	// This helper deliberately does not decode, authenticate or grant authority.
	if err := os.WriteFile(path, []byte("changed metadata bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	repeated, err := FingerprintMountedPublicMetadata(t.Context(), path)
	if err != nil || repeated == observed {
		t.Fatal("changed public metadata kept old digest")
	}
	for _, invalid := range []string{"relative.json", path + ".missing", directory} {
		if _, err := FingerprintMountedPublicMetadata(t.Context(), invalid); err == nil || errors.Is(err, ErrMountedDatabaseTrustLost) {
			t.Fatal("metadata failure became success or physical signing loss")
		}
	}
	link := filepath.Join(directory, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := FingerprintMountedPublicMetadata(t.Context(), link); err == nil || errors.Is(err, ErrMountedDatabaseTrustLost) {
		t.Fatal("metadata symlink granted signing-loss fact")
	}
	if err := os.WriteFile(path, make([]byte, maxTrustKeyBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := FingerprintMountedPublicMetadata(t.Context(), path); err == nil || errors.Is(err, ErrMountedDatabaseTrustLost) {
		t.Fatal("oversized metadata qualified")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := FingerprintMountedPublicMetadata(canceled, path); !errors.Is(err, context.Canceled) {
		t.Fatal("metadata hash lost cancellation")
	}
	if _, err := FingerprintMountedPublicMetadata(nil, path); err == nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
		t.Fatal("nil context accepted")
	}
}
