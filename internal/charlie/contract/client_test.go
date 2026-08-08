package contract

import (
	"crypto/tls"
	"crypto/x509"
	"net/url"
	"testing"
)

const testBridgeServerIdentity = "spiffe://astronomer.local/installations/installation-a/charlie-agent-bridge"

type availability bool

func (a availability) AllowsConfiguration() bool { return bool(a) }

func testTLSConfig() *tls.Config {
	return &tls.Config{
		RootCAs: x509.NewCertPool(),
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{{1}},
		}},
	}
}

func TestNewLocalClientTargetsOnlyAgentService(t *testing.T) {
	client, err := NewLocalClient(availability(true), "charlie-system", testBridgeServerIdentity, testTLSConfig())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := client.Endpoint(), "https://charlie-agent-bridge.charlie-system.svc:7443/bridge/v1"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
	if got, want := client.transport.TLSClientConfig.ServerName, "charlie-agent-bridge.charlie-system.svc"; got != want {
		t.Fatalf("TLS server name = %q, want %q", got, want)
	}
	if client.transport.TLSClientConfig.MinVersion != tls.VersionTLS13 || client.transport.Proxy != nil {
		t.Fatal("local bridge transport is not TLS 1.3-only and proxy-free")
	}
	for _, namespace := range []string{"", "Charlie", "namespace.example", "../central", "a/b"} {
		if _, err := NewLocalClient(availability(true), namespace, testBridgeServerIdentity, testTLSConfig()); err == nil {
			t.Errorf("namespace %q unexpectedly accepted", namespace)
		}
	}
}

func TestNewLocalClientRequiresMutualTLS(t *testing.T) {
	for _, config := range []*tls.Config{nil, {}, {RootCAs: x509.NewCertPool()}} {
		if _, err := NewLocalClient(availability(true), "charlie-system", testBridgeServerIdentity, config); err == nil {
			t.Fatal("client accepted incomplete mutual TLS configuration")
		}
	}
}

func TestNewLocalClientRequiresFeatureAvailability(t *testing.T) {
	if _, err := NewLocalClient(availability(false), "charlie-system", testBridgeServerIdentity, testTLSConfig()); err == nil {
		t.Fatal("client constructed while feature unavailable")
	}
	if _, err := NewLocalClient(nil, "charlie-system", testBridgeServerIdentity, testTLSConfig()); err == nil {
		t.Fatal("client constructed without backend availability")
	}
}

func TestLocalClientRequiresExactInstallationIdentity(t *testing.T) {
	client, err := NewLocalClient(availability(true), "charlie-system", testBridgeServerIdentity, testTLSConfig())
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := url.Parse(testBridgeServerIdentity)
	leaf := &x509.Certificate{URIs: []*url.URL{identity}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	state := tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: [][]*x509.Certificate{{leaf}}}
	if err := client.transport.TLSClientConfig.VerifyConnection(state); err != nil {
		t.Fatalf("exact installation identity rejected: %v", err)
	}
	wrong, _ := url.Parse("spiffe://astronomer.local/installations/other/charlie-agent-bridge")
	leaf.URIs = []*url.URL{wrong}
	if err := client.transport.TLSClientConfig.VerifyConnection(state); err == nil {
		t.Fatal("wrong installation identity accepted")
	}
	if _, err := NewLocalClient(availability(true), "charlie-system", "https://central.example", testTLSConfig()); err == nil {
		t.Fatal("non-SPIFFE server identity accepted")
	}
}
