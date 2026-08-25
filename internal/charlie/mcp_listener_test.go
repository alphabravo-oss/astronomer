package charlie

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
		ExpectedClientURI: testMCPClientURI,
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

func TestMCPListenerRejectsInvalidMutualTLSIdentities(t *testing.T) {
	now := time.Now().UTC()
	trustedCAPEM, _, trustedCA, trustedCAKey, err := createLocalCA(now)
	if err != nil {
		t.Fatal(err)
	}
	serverCertPEM, serverKeyPEM, err := issueLeaf(trustedCA, trustedCAKey, now, "astronomer-mcp-server", []string{"astronomer-charlie-mcp.astronomer.svc"}, "spiffe://astronomer.local/installations/test/mcp-server", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	if err != nil {
		t.Fatal(err)
	}
	validCertPEM, validKeyPEM, err := issueLeaf(trustedCA, trustedCAKey, now, "charlie-mcp-client", nil, testMCPClientURI, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	if err != nil {
		t.Fatal(err)
	}
	wrongURICertPEM, wrongURIKeyPEM, err := issueLeaf(trustedCA, trustedCAKey, now, "wrong-charlie-client", nil, "spiffe://astronomer.local/installations/other/mcp-client", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	if err != nil {
		t.Fatal(err)
	}
	expiredAt := now.Add(-leafValidity - time.Hour)
	expiredCertPEM, expiredKeyPEM, err := issueLeaf(trustedCA, trustedCAKey, expiredAt, "expired-charlie-client", nil, testMCPClientURI, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	if err != nil {
		t.Fatal(err)
	}
	_, _, untrustedCA, untrustedCAKey, err := createLocalCA(now)
	if err != nil {
		t.Fatal(err)
	}
	untrustedCertPEM, untrustedKeyPEM, err := issueLeaf(untrustedCA, untrustedCAKey, now, "untrusted-charlie-client", nil, testMCPClientURI, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	if err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	certPath := filepath.Join(directory, "tls.crt")
	keyPath := filepath.Join(directory, "tls.key")
	caPath := filepath.Join(directory, "client-ca.crt")
	for path, value := range map[string]string{certPath: serverCertPEM, keyPath: serverKeyPEM, caPath: trustedCAPEM} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	handler, _, _ := testMCPHandler(t, allowedWriteFacts(ModeAuto))
	listener, err := NewMCPListener(MCPListenerConfig{
		Address: "127.0.0.1:0", Certificate: certPath, PrivateKey: keyPath, ClientCA: caPath,
		ExpectedClientURI: testMCPClientURI,
	}, handler)
	if err != nil {
		t.Fatal(err)
	}
	listener.server.ErrorLog = log.New(io.Discard, "", 0)
	rawListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = listener.server.Serve(tls.NewListener(rawListener, listener.server.TLSConfig)) }()
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = listener.Shutdown(shutdownContext)
	})

	serverRoots := x509.NewCertPool()
	if !serverRoots.AppendCertsFromPEM([]byte(trustedCAPEM)) {
		t.Fatal("could not create test server trust")
	}
	clientPair := func(certPEM, keyPEM string) tls.Certificate {
		pair, pairErr := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
		if pairErr != nil {
			t.Fatal(pairErr)
		}
		return pair
	}
	request := func(pair tls.Certificate) (int, error) {
		transport := &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, RootCAs: serverRoots,
			ServerName: "astronomer-charlie-mcp.astronomer.svc", Certificates: []tls.Certificate{pair},
		}}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
		req, requestErr := http.NewRequest(http.MethodPost, "https://"+rawListener.Addr().String()+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":"one","method":"tools/list","params":{}}`))
		if requestErr != nil {
			return 0, requestErr
		}
		req.Header.Set("Content-Type", "application/json")
		response, requestErr := client.Do(req)
		if requestErr != nil {
			return 0, requestErr
		}
		defer func() { _ = response.Body.Close() }()
		return response.StatusCode, nil
	}

	if status, err := request(clientPair(validCertPEM, validKeyPEM)); err != nil || status != http.StatusOK {
		t.Fatalf("valid client failed: status=%d err=%v", status, err)
	}
	for name, pair := range map[string]tls.Certificate{
		"untrusted_ca": clientPair(untrustedCertPEM, untrustedKeyPEM),
		"expired":      clientPair(expiredCertPEM, expiredKeyPEM),
		"wrong_uri":    clientPair(wrongURICertPEM, wrongURIKeyPEM),
	} {
		t.Run(name, func(t *testing.T) {
			if status, err := request(pair); err == nil {
				t.Fatalf("invalid mTLS identity reached HTTP with status %d", status)
			}
		})
	}
}

func TestMCPListenerRefusesMissingTLSMaterial(t *testing.T) {
	facts := allowedWriteFacts(ModeAuto)
	handler, _, _ := testMCPHandler(t, facts)
	if _, err := NewMCPListener(MCPListenerConfig{Address: ":7444"}, handler); err == nil {
		t.Fatal("listener accepted missing TLS material")
	}
}
