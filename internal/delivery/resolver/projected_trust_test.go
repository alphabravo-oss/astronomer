package resolver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

func TestProjectedSecretTrustVerifiesAndRejectsSymlinkedFiles(t *testing.T) {
	t.Parallel()
	artifact := []byte("projected-release-evidence")
	policy := model.TrustPolicy{Provider: model.SignatureCosignKeyless, Identity: "release@example.test", Issuer: "https://issuer.example.test"}
	fixture := newKeylessFixture(t, artifact, policy.Identity, policy.Issuer)
	projected := t.TempDir()
	if err := os.Symlink(fixture.trustDir, filepath.Join(projected, "..data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..data/trusted_root.json", filepath.Join(projected, "trusted_root.json")); err != nil {
		t.Fatal(err)
	}
	outer, err := NewExecVerifier(projected)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := outer.VerifyOfflineMetadata(t.Context(), artifact, fixture.protoBundle, policy); err == nil {
		t.Fatal("symlinked trust file accepted")
	}
	current, err := NewExecVerifier(filepath.Join(projected, "..data"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := current.VerifyOfflineMetadata(t.Context(), artifact, fixture.protoBundle, policy); err != nil {
		t.Fatalf("projected Secret data failed offline verification: %v", err)
	}
	if _, err := current.VerifyOfflineMetadata(t.Context(), []byte("tampered"), fixture.protoBundle, policy); err == nil {
		t.Fatal("tampered evidence accepted")
	}
}
