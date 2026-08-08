package charlie

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMCPListenerIsPrivateMutualTLS13AndReloadable(t *testing.T) {
	now := time.Now().UTC()
	caPEM, _, ca, caKey, err := createLocalCA(now)
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := issueLeaf(ca, caKey, now, "astronomer-mcp-server", []string{"astronomer-charlie-mcp.astronomer.svc"}, "spiffe://astronomer.local/installations/test/mcp-server", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	certPath := filepath.Join(directory, "tls.crt")
	keyPath := filepath.Join(directory, "tls.key")
	caPath := filepath.Join(directory, "client-ca.crt")
	for path, value := range map[string]string{certPath: certPEM, keyPath: keyPEM, caPath: caPEM} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	facts := allowedWriteFacts(ModeAuto)
	handler, _, _ := testMCPHandler(t, facts)
	listener, err := NewMCPListener(MCPListenerConfig{
		Address: "127.0.0.1:0", Certificate: certPath, PrivateKey: keyPath, ClientCA: caPath,
	}, handler)
	if err != nil {
		t.Fatal(err)
	}
	config := listener.server.TLSConfig
	if config.MinVersion != tls.VersionTLS13 || config.ClientAuth != tls.RequireAndVerifyClientCert || config.GetCertificate == nil || len(config.Certificates) != 0 {
		t.Fatal("MCP listener does not enforce reloadable TLS 1.3 mutual authentication")
	}
	first, err := listener.reload.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	secondCertPEM, secondKeyPEM, err := issueLeaf(ca, caKey, now.Add(time.Minute), "astronomer-mcp-server", []string{"astronomer-charlie-mcp.astronomer.svc"}, "spiffe://astronomer.local/installations/test/mcp-server", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, []byte(secondCertPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte(secondKeyPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := listener.reload.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Certificate[0]) == string(second.Certificate[0]) {
		t.Fatal("mounted MCP certificate rotation was not reloaded")
	}
}

func TestMCPListenerRefusesMissingTLSMaterial(t *testing.T) {
	facts := allowedWriteFacts(ModeAuto)
	handler, _, _ := testMCPHandler(t, facts)
	if _, err := NewMCPListener(MCPListenerConfig{Address: ":7444"}, handler); err == nil {
		t.Fatal("listener accepted missing TLS material")
	}
}
