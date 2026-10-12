//go:build unix

package resolver

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseRegistryPublicRootsCopyPreservesExactTransportIdentity(t *testing.T) {
	options, requests, roots := registryTrustFixture(t)
	transport, err := CaptureDatabaseRegistryTransport(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	copied, err := transport.CertificateAuthorities(t.Context())
	if err != nil || !bytes.Equal(copied, roots) {
		t.Fatal("exact captured roots unavailable")
	}
	copied[0] ^= 1
	again, err := transport.CertificateAuthorities(t.Context())
	if err != nil || !bytes.Equal(again, roots) {
		t.Fatal("caller altered captured TLS material")
	}
	if requests.Load() != 0 {
		t.Fatal("public root copy fetched registry content")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if material, err := transport.CertificateAuthorities(ctx); material != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled root copy leaked material or cancellation")
	}
	if err := os.WriteFile(filepath.Join(options.TrustDirectory, "registry.pem"), []byte("changed roots"), 0600); err != nil {
		t.Fatal(err)
	}
	if material, err := transport.CertificateAuthorities(t.Context()); material != nil || err == nil {
		t.Fatal("mounted rotation ignored for provider material")
	}
	transport.Close()
	if material, err := transport.CertificateAuthorities(t.Context()); material != nil || err == nil {
		t.Fatal("closed transport retained public material access")
	}
}
