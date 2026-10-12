//go:build unix

package distribution

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseAuthorityRegistryRootsRequireExactCurrentProof(t *testing.T) {
	options, registry, _, roots := pinnedDatabaseAuthorityFixture(t)
	authority, err := NewPinnedDatabaseAuthority(t.Context(), options, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()
	proof, err := authority.Base(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	material, err := authority.RegistryCertificateAuthorities(t.Context(), proof)
	if err != nil || !bytes.Equal(material, roots) {
		t.Fatal("provider roots differ from actual captured transport")
	}
	material[0] ^= 1
	copy, err := authority.RegistryCertificateAuthorities(t.Context(), proof)
	if err != nil || !bytes.Equal(copy, roots) {
		t.Fatal("caller roots mutated authority")
	}
	if material, err := authority.RegistryCertificateAuthorities(t.Context(), &VerifiedDatabaseAuthorityBase{}); material != nil || err == nil {
		t.Fatal("foreign proof received constructor material")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if material, err := authority.RegistryCertificateAuthorities(ctx, proof); material != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled proof yielded public roots")
	}
	if err := os.WriteFile(filepath.Join(registry.TrustDirectory, registry.RootsRef+".pem"), []byte("removed roots"), 0600); err != nil {
		t.Fatal(err)
	}
	if material, err := authority.RegistryCertificateAuthorities(t.Context(), proof); material != nil || err == nil {
		t.Fatal("mounted roots changed before provider construction")
	}
	authority.Close()
	if material, err := authority.RegistryCertificateAuthorities(t.Context(), proof); material != nil || err == nil {
		t.Fatal("closed proof yielded public roots")
	}
}
