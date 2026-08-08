package charlie

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

func TestGenerateLocalTrustRequiresEncryptionAndExactDistinctDNS(t *testing.T) {
	installationID := "3c608d44-848c-45d6-bd86-246be0b880af"
	if _, err := GenerateLocalTrust(nil, LocalTrustConfig{InstallationID: installationID, BridgeServerDNS: "bridge.ns.svc", MCPServerDNS: "mcp.ns.svc"}); err == nil {
		t.Fatal("generated reusable trust without an encryptor")
	}
	key, _ := auth.GenerateKey()
	encryptor, _ := auth.NewEncryptor(key)
	for _, cfg := range []LocalTrustConfig{
		{InstallationID: installationID, BridgeServerDNS: "*.ns.svc", MCPServerDNS: "mcp.ns.svc"},
		{InstallationID: installationID, BridgeServerDNS: "same.ns.svc", MCPServerDNS: "same.ns.svc"},
		{InstallationID: installationID, BridgeServerDNS: "https://bridge", MCPServerDNS: "mcp.ns.svc"},
		{InstallationID: "not-a-uuid", BridgeServerDNS: "bridge.ns.svc", MCPServerDNS: "mcp.ns.svc"},
	} {
		if _, err := GenerateLocalTrust(encryptor, cfg); err == nil {
			t.Fatalf("unsafe service identities accepted: %+v", cfg)
		}
	}
}

func TestGenerateLocalTrustSeparatesEKUsSANsAndStorage(t *testing.T) {
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	trust, err := GenerateLocalTrust(encryptor, LocalTrustConfig{
		InstallationID:  "3c608d44-848c-45d6-bd86-246be0b880af",
		BridgeServerDNS: "charlie-agent.charlie-system.svc",
		MCPServerDNS:    "astronomer-charlie-mcp.astronomer.svc",
		Now:             now,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertLeaf := func(name, certificatePEM string, usage x509.ExtKeyUsage, dns, identity string) {
		t.Helper()
		block, _ := pem.Decode([]byte(certificatePEM))
		if block == nil {
			t.Fatalf("%s certificate is not PEM", name)
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		if len(certificate.ExtKeyUsage) != 1 || certificate.ExtKeyUsage[0] != usage {
			t.Fatalf("%s EKU=%v, want only %v", name, certificate.ExtKeyUsage, usage)
		}
		if dns == "" && len(certificate.DNSNames) != 0 {
			t.Fatalf("%s unexpectedly has DNS SANs %v", name, certificate.DNSNames)
		}
		if dns != "" && (len(certificate.DNSNames) != 1 || certificate.DNSNames[0] != dns) {
			t.Fatalf("%s DNS SANs=%v, want %s", name, certificate.DNSNames, dns)
		}
		if len(certificate.URIs) != 1 || certificate.URIs[0].String() != identity {
			t.Fatalf("%s URI SANs=%v, want %s", name, certificate.URIs, identity)
		}
		if !certificate.NotAfter.Equal(now.Add(leafValidity)) {
			t.Fatalf("%s expiry=%s", name, certificate.NotAfter)
		}
	}
	assertLeaf("bridge client", trust.Public.BridgeClientCertificate, x509.ExtKeyUsageClientAuth, "", trust.Public.BridgeClientIdentityURI)
	assertLeaf("bridge server", trust.Agent.BridgeServerCertificate, x509.ExtKeyUsageServerAuth, "charlie-agent.charlie-system.svc", trust.Agent.BridgeServerIdentityURI)
	assertLeaf("MCP server", trust.Public.MCPServerCertificate, x509.ExtKeyUsageServerAuth, "astronomer-charlie-mcp.astronomer.svc", trust.Public.MCPServerIdentityURI)
	assertLeaf("MCP client", trust.Agent.MCPClientCertificate, x509.ExtKeyUsageClientAuth, "", trust.Agent.MCPClientIdentityURI)

	plaintext, err := encryptor.Decrypt(trust.EncryptedLocalTrust)
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]string
	if err := json.Unmarshal([]byte(plaintext), &persisted); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ca_private_key_pem", "bridge_client_private_key_pem", "mcp_server_private_key_pem"} {
		if !strings.Contains(persisted[key], "PRIVATE KEY") {
			t.Fatalf("encrypted trust missing %s", key)
		}
	}
	if len(persisted) != 3 {
		t.Fatalf("unexpected material persisted: %v", persisted)
	}
	serialized, err := json.Marshal(trust)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{trust.Agent.BridgeServerPrivateKey, trust.Agent.MCPClientPrivateKey} {
		if strings.Contains(string(serialized), secret) {
			t.Fatal("agent private key leaked through serialization")
		}
	}
}

func TestRotateLocalTrustUsesExactDualTrustOverlapAndPrunes(t *testing.T) {
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	cfg := LocalTrustConfig{
		InstallationID:  "3c608d44-848c-45d6-bd86-246be0b880af",
		BridgeServerDNS: "charlie-agent.charlie-system.svc",
		MCPServerDNS:    "astronomer-charlie-mcp.astronomer.svc",
		Now:             base,
	}
	previous, err := GenerateLocalTrust(encryptor, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Now = base.Add(time.Hour)
	rotation, err := RotateLocalTrust(encryptor, cfg, previous)
	if err != nil {
		t.Fatal(err)
	}
	if !rotation.OverlapUntil.Equal(cfg.Now.Add(24 * time.Hour)) {
		t.Fatalf("overlap until=%s", rotation.OverlapUntil)
	}
	if strings.Count(rotation.Public.CACertificatePEM, "BEGIN CERTIFICATE") != 2 ||
		strings.Count(rotation.Agent.CACertificatePEM, "BEGIN CERTIFICATE") != 2 {
		t.Fatal("rotation did not publish both CA certificates")
	}
	if _, changed, err := PruneExpiredPreviousTrust(encryptor, rotation.EncryptedLocalTrust, rotation.OverlapUntil.Add(-time.Nanosecond)); err != nil || changed {
		t.Fatalf("previous trust pruned during overlap: changed=%t err=%v", changed, err)
	}
	pruned, changed, err := PruneExpiredPreviousTrust(encryptor, rotation.EncryptedLocalTrust, rotation.OverlapUntil)
	if err != nil || !changed {
		t.Fatalf("previous trust not pruned at expiry: changed=%t err=%v", changed, err)
	}
	persisted, err := decryptPersistedTrust(encryptor, pruned)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Previous != nil || persisted.PreviousTrustExpiresAt != nil {
		t.Fatal("expired previous private trust remains encrypted")
	}
}
