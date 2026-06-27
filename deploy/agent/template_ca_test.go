package agenttemplate

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"
)

func selfSignedPEM(t *testing.T) (pemStr, checksum string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "render-test-ca"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("createcert: %v", err)
	}
	pemStr = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	checksum = fmt.Sprintf("%x", sha256.Sum256(der))
	return pemStr, checksum
}

// TestCAChecksumFromPEM verifies the checksum helper hashes the cert DER.
func TestCAChecksumFromPEM(t *testing.T) {
	pemStr, want := selfSignedPEM(t)
	if got := CAChecksumFromPEM(pemStr); got != want {
		t.Fatalf("CAChecksumFromPEM = %q, want %q", got, want)
	}
	if got := CAChecksumFromPEM(""); got != "" {
		t.Fatalf("empty PEM should yield empty checksum, got %q", got)
	}
	if got := CAChecksumFromPEM("garbage"); got != "" {
		t.Fatalf("unparseable PEM should yield empty checksum, got %q", got)
	}
}

// TestRenderInstallYAML_CAPopulated proves that when a CA bundle is supplied the
// rendered manifest carries the base64 CA in the Secret and the hex checksum in
// the ASTRONOMER_CA_CHECKSUM env. FAILS without the CA wiring.
func TestRenderInstallYAML_CAPopulated(t *testing.T) {
	pemStr, checksum := selfSignedPEM(t)
	manifest := RenderInstallYAML(InstallTemplateData{
		ServerURL:         "https://astro.example.com",
		ClusterID:         "550e8400-e29b-41d4-a716-446655440000",
		RegistrationToken: "reg-token",
		CACert:            pemStr,
		CAChecksum:        checksum,
		AgentImage:        "example.com/agent:v1",
	})

	wantB64 := base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(pemStr)))
	if !strings.Contains(manifest, "ca.crt: \""+wantB64+"\"") {
		t.Fatalf("manifest CA Secret did not carry base64 PEM; want ca.crt: %q", wantB64)
	}
	if !strings.Contains(manifest, "name: ASTRONOMER_CA_CHECKSUM") {
		t.Fatal("manifest missing ASTRONOMER_CA_CHECKSUM env entry")
	}
	if !strings.Contains(manifest, "value: \""+checksum+"\"") {
		t.Fatalf("manifest missing checksum env value %q", checksum)
	}
}

// TestRenderInstallYAML_CAEmpty proves the default (no-CA) path renders an empty
// CA Secret field and an empty checksum env — byte-for-byte the pre-change
// no-CA shape, so a no-CA agent still falls back to OS trust.
func TestRenderInstallYAML_CAEmpty(t *testing.T) {
	manifest := RenderInstallYAML(InstallTemplateData{
		ServerURL:         "https://astro.example.com",
		ClusterID:         "550e8400-e29b-41d4-a716-446655440000",
		RegistrationToken: "reg-token",
		AgentImage:        "example.com/agent:v1",
	})
	if !strings.Contains(manifest, `ca.crt: ""`) {
		t.Fatal("no-CA manifest should render empty ca.crt Secret field")
	}
	if !strings.Contains(manifest, `value: ""`) {
		t.Fatal("no-CA manifest should render empty ASTRONOMER_CA_CHECKSUM env value")
	}
}
