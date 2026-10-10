//go:build !unix

package resolver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

func TestOrdinaryTrustReaderPreservedWhileStrictAuthorityUnsupported(t *testing.T) {
	directory := t.TempDir()
	material := []byte("ordinary reader bytes; crypto parser is a separate stage")
	if err := os.WriteFile(filepath.Join(directory, "qa.pub"), material, 0600); err != nil {
		t.Fatal(err)
	}
	verifier, err := NewExecVerifier(directory)
	if err != nil {
		t.Fatal(err)
	}
	observed, _, err := verifier.readTrustKey("qa", ".pub")
	if err != nil || string(observed) != string(material) {
		t.Fatal("ordinary platform reader changed")
	}
	policy := model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"}
	if snapshot, err := CaptureCosignTrust(t.Context(), directory, policy); err == nil || snapshot != nil {
		t.Fatal("unqualified strict platform granted authority")
	}
}
