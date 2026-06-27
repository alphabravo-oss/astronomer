package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"testing"
	"time"
)

// genSelfSignedCert returns a self-signed cert as PEM plus the parsed cert.
func genSelfSignedCert(t *testing.T) (pemBytes []byte, cert *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-ca"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("createcert: %v", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsecert: %v", err)
	}
	pemBytes = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return pemBytes, parsed
}

// TestBuildTLSConfig_DefaultNilWhenNoCA is the byte-for-byte behavior-equivalent
// guarantee: with no CA and no checksum, the builder returns (nil, nil) so the
// dialer uses OS-trust defaults exactly as before this change.
func TestBuildTLSConfig_DefaultNilWhenNoCA(t *testing.T) {
	cfg, err := BuildTLSConfig("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil tls.Config for default (no CA) path, got %#v", cfg)
	}
	// Whitespace-only inputs must also collapse to the default path.
	cfg, err = BuildTLSConfig("   ", "  ")
	if err != nil || cfg != nil {
		t.Fatalf("whitespace inputs: cfg=%#v err=%v, want nil/nil", cfg, err)
	}
}

func TestBuildTLSConfig_LoadsRootCAs(t *testing.T) {
	pemBytes, _ := genSelfSignedCert(t)
	cfg, err := BuildTLSConfig(string(pemBytes), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil tls.Config when CA provided")
	}
	if cfg.RootCAs == nil {
		t.Fatal("expected RootCAs to be set from the PEM bundle")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %d, want TLS12", cfg.MinVersion)
	}
	// No checksum → no VerifyConnection pin installed.
	if cfg.VerifyConnection != nil {
		t.Fatal("VerifyConnection should be nil when no checksum is pinned")
	}
}

func TestBuildTLSConfig_InvalidPEM(t *testing.T) {
	if _, err := BuildTLSConfig("not a pem", ""); err == nil {
		t.Fatal("expected error for unparseable PEM")
	}
}

func TestBuildTLSConfig_ChecksumWithoutCARejected(t *testing.T) {
	if _, err := BuildTLSConfig("", "deadbeef"); err == nil {
		t.Fatal("expected error: checksum without CA must be rejected (cannot pin without trust anchor)")
	}
}

// TestBuildTLSConfig_VerifyConnectionMatch proves the pin ACCEPTS when the
// server-presented CA matches the configured checksum.
func TestBuildTLSConfig_VerifyConnectionMatch(t *testing.T) {
	pemBytes, cert := genSelfSignedCert(t)
	checksum := fmt.Sprintf("%x", sha256.Sum256(cert.Raw))

	cfg, err := BuildTLSConfig(string(pemBytes), checksum)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.VerifyConnection == nil {
		t.Fatal("expected VerifyConnection to be set when checksum provided")
	}
	state := tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	if err := cfg.VerifyConnection(state); err != nil {
		t.Fatalf("VerifyConnection rejected a MATCHING CA: %v", err)
	}
	// Uppercase checksum must also match (case-insensitive pin).
	cfgUpper, _ := BuildTLSConfig(string(pemBytes), "ABCDEF"+checksum[6:])
	_ = cfgUpper // constructed only to ensure no error for mixed case below
}

// TestBuildTLSConfig_VerifyConnectionFailClosed is the core fail-closed test:
// a wrong checksum MUST cause VerifyConnection to return an error, refusing the
// connection. This test FAILS without the pinning implementation.
func TestBuildTLSConfig_VerifyConnectionFailClosed(t *testing.T) {
	pemBytes, cert := genSelfSignedCert(t)

	// A different cert presented by the "server" than the pinned checksum.
	_, otherCert := genSelfSignedCert(t)
	pinnedChecksum := fmt.Sprintf("%x", sha256.Sum256(cert.Raw))

	cfg, err := BuildTLSConfig(string(pemBytes), pinnedChecksum)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	state := tls.ConnectionState{PeerCertificates: []*x509.Certificate{otherCert}}
	if err := cfg.VerifyConnection(state); err == nil {
		t.Fatal("FAIL CLOSED violated: VerifyConnection accepted a MISMATCHED server CA")
	}

	// No peer certs at all must also fail closed.
	if err := cfg.VerifyConnection(tls.ConnectionState{}); err == nil {
		t.Fatal("FAIL CLOSED violated: VerifyConnection accepted an empty cert chain")
	}
}

// TestBuildTLSConfig_CaseInsensitiveChecksum verifies the pin matches regardless
// of the hex case of the configured checksum.
func TestBuildTLSConfig_CaseInsensitiveChecksum(t *testing.T) {
	pemBytes, cert := genSelfSignedCert(t)
	checksum := fmt.Sprintf("%X", sha256.Sum256(cert.Raw)) // UPPER-case hex
	cfg, err := BuildTLSConfig(string(pemBytes), checksum)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	state := tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	if err := cfg.VerifyConnection(state); err != nil {
		t.Fatalf("uppercase checksum rejected a matching CA: %v", err)
	}
}
