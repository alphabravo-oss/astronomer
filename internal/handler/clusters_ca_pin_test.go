package handler

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func caPinTestCert(t *testing.T) (pemStr, checksum string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "registration-ca"},
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

// TestRenderAgentInstallManifest_CAPinPopulated proves the renderer pulls the CA
// bundle from platform_settings[registration.ca_bundle], base64s it into the CA
// Secret, and renders the matching hex SHA-256 into ASTRONOMER_CA_CHECKSUM.
// FAILS without the renderer wiring (which previously hardcoded CACert: "").
func TestRenderAgentInstallManifest_CAPinPopulated(t *testing.T) {
	caPEM, checksum := caPinTestCert(t)

	// platform_settings.value is JSONB carrying a JSON-encoded string.
	jsonVal, err := json.Marshal(caPEM)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	q := &clusterRegistryTestQuerier{
		platformSettings: map[string]sqlc.PlatformSetting{
			"registration.ca_bundle": {Key: "registration.ca_bundle", Value: jsonVal},
		},
	}
	h := NewClusterHandler(q)
	h.SetAgentImage("example.com/astronomer-agent", "v1.2.3")

	cluster := sqlc.Cluster{ID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"), Name: "demo"}
	manifest := h.renderAgentInstallManifest(cluster, "reg-token", "https://astro.example.com")

	wantB64 := base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(caPEM)))
	if !strings.Contains(manifest, "ca.crt: \""+wantB64+"\"") {
		t.Fatalf("manifest CA Secret missing base64 PEM:\n%s", manifest)
	}
	if !strings.Contains(manifest, "value: \""+checksum+"\"") {
		t.Fatalf("manifest missing ASTRONOMER_CA_CHECKSUM value %q", checksum)
	}
}

// TestRenderAgentInstallManifest_CAEmptyByDefault proves the default (no CA in
// platform_settings) path leaves the CA Secret and checksum env empty, so a
// no-CA agent stays on the OS-trust path — byte-for-byte the prior behavior.
func TestRenderAgentInstallManifest_CAEmptyByDefault(t *testing.T) {
	q := &clusterRegistryTestQuerier{} // GetPlatformSetting returns ErrNoRows
	h := NewClusterHandler(q)
	h.SetAgentImage("example.com/astronomer-agent", "v1.2.3")

	cluster := sqlc.Cluster{ID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"), Name: "demo"}
	manifest := h.renderAgentInstallManifest(cluster, "reg-token", "https://astro.example.com")

	if !strings.Contains(manifest, `ca.crt: ""`) {
		t.Fatalf("expected empty ca.crt Secret field for no-CA path:\n%s", manifest)
	}
	if !strings.Contains(manifest, "name: ASTRONOMER_CA_CHECKSUM") || !strings.Contains(manifest, `value: ""`) {
		t.Fatalf("expected empty ASTRONOMER_CA_CHECKSUM env for no-CA path:\n%s", manifest)
	}
}
