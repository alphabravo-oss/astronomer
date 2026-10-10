package helmruntime

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	repo "helm.sh/helm/v4/pkg/repo/v1"
)

// This exact source fixture is intended to characterize the current transport
// BEFORE the separately drafted first-hop TLS identity refinement. It requires
// the declared owned private address and must not be counted green on a skip.
func TestHTTPSHelmProxyDoesNotReceiveRepositoryClientCertificate(t *testing.T) {
	address := os.Getenv("ASTRONOMER_HELM_HTTP_PRIVATE_FIXTURE_IP")
	if address == "" {
		t.Skip("owned private Helm proxy address is not configured")
	}
	ip := net.ParseIP(address)
	if ip == nil || !ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		t.Fatal("owned private fixture address is invalid")
	}
	serverCertificate, ca, clientPEM, keyPEM := proxyIdentityCertificates(t, ip)
	root := t.TempDir()
	owned := newProxyTunnelOwnership()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("fixture CA is invalid")
	}
	var targetIdentities, proxyIdentities atomic.Int32
	target := privateTLSProxyServer(t, address, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverCertificate}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.PeerCertificates) != 1 || r.TLS.PeerCertificates[0].Subject.CommonName != "repository-client" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		targetIdentities.Add(1)
		w.Header().Set("Connection", "close") // This one-shot fixture owns no reusable tunnel.
		_, _ = w.Write([]byte("fixture chart bytes"))
	}))
	targetHost, _, err := net.SplitHostPort(target.Listener.Addr().String())
	if err != nil || targetHost != address {
		t.Fatal("target listener ownership changed")
	}
	drained := make(chan struct{}, 1)
	proxy := privateTLSProxyServer(t, address, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverCertificate}, ClientAuth: tls.RequestClientCert}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != target.Listener.Addr().String() {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if len(r.TLS.PeerCertificates) > 0 {
			proxyIdentities.Add(1)
		}
		tunnel, ok := owned.begin(r.Context())
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		defer owned.finish(tunnel)
		upstream, err := (&net.Dialer{Timeout: time.Second}).DialContext(tunnel.ctx, "tcp", target.Listener.Addr().String())
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if !owned.connection(tunnel, upstream) {
			return
		}
		downstream, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			closeProxyConnection(upstream)
			return
		}
		if !owned.connection(tunnel, downstream) {
			return
		}
		defer func() {
			closeProxyConnection(upstream)
			closeProxyConnection(downstream)
			select {
			case drained <- struct{}{}:
			default:
			}
		}()
		if _, err := buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err := buffer.Flush(); err != nil {
			return
		}
		_ = upstream.SetDeadline(time.Now().Add(3 * time.Second))
		_ = downstream.SetDeadline(time.Now().Add(3 * time.Second))
		copied := make(chan struct{})
		go func() {
			_, _ = io.Copy(upstream, buffer)
			closeProxyConnection(upstream)
			closeProxyConnection(downstream)
			close(copied)
		}()
		_, _ = io.Copy(downstream, upstream)
		closeProxyConnection(upstream)
		closeProxyConnection(downstream)
		<-copied
	}))
	// Registered after both servers, before any request: joins hijacked workers
	// before httptest.Close or private TempDir removal, even on expected FAIL.
	t.Cleanup(func() {
		if !owned.closeAndJoin(4 * time.Second) {
			t.Error("owned proxy handlers/copy workers did not drain")
		}
	})
	caFile := filepath.Join(root, "ca.pem")
	certFile := filepath.Join(root, "client.pem")
	keyFile := filepath.Join(root, "client-key.pem")
	for name, value := range map[string][]byte{caFile: ca, certFile: clientPEM, keyFile: keyPEM} {
		if err := os.WriteFile(name, value, 0o600); err != nil {
			t.Fatal("private fixture trust file creation failed")
		}
	}
	bits := 128
	if ip.To4() != nil {
		bits = 32
	}
	cidr := fmt.Sprintf("%s/%d", address, bits)
	runtime := Config{RepositoryProxyURL: proxy.URL, RepositoryDestinationPolicy: fmt.Sprintf(`{"origins":[{"url":%q,"cidrs":[%q]},{"url":%q,"cidrs":[%q]}]}`, target.URL, cidr, proxy.URL, cidr)}
	owner := &repo.Entry{Name: "owned", URL: target.URL, CAFile: caFile, CertFile: certFile, KeyFile: keyFile}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	getter := repositoryGetter{ctx: ctx, runtime: runtime, owner: owner}
	if value, err := getter.Get(target.URL + "/private-chart.tgz"); err != nil || value.String() != "fixture chart bytes" {
		t.Fatal("actual private target mutual TLS transfer failed")
	}
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("proxy tunnel worker did not join after transfer")
	}
	if targetIdentities.Load() != 1 {
		t.Fatal("target repository client identity was lost")
	}
	if proxyIdentities.Load() != 0 {
		t.Fatal("HTTPS proxy received repository client TLS identity")
	}
}

func privateTLSProxyServer(t *testing.T, address string, configuration *tls.Config, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp", net.JoinHostPort(address, "0"))
	if err != nil {
		t.Fatal("owned fixture listener failed")
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.TLS = configuration
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func proxyIdentityCertificates(t *testing.T, ip net.IP) (tls.Certificate, []byte, []byte, []byte) {
	t.Helper()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("fixture key generation failed")
	}
	now := time.Now()
	rootProfile := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "owned-proxy-fixture"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IPAddresses: []net.IP{ip}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootProfile, rootProfile, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal("fixture CA signing failed")
	}
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("fixture client key failed")
	}
	clientProfile := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "repository-client"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientProfile, rootProfile, &clientKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal("fixture client signing failed")
	}
	encodedKey, err := x509.MarshalPKCS8PrivateKey(clientKey)
	if err != nil {
		t.Fatal("fixture private key encoding failed")
	}
	return tls.Certificate{Certificate: [][]byte{rootDER}, PrivateKey: rootKey}, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey})
}

// A tracked handler cannot be admitted after cleanup begins. Each handler joins
// its two copy workers before finish; cancellation closes both sockets, including
// connections arriving during cleanup. No hijacked socket is owned by Server.Close.
type proxyOwnedTunnel struct {
	ctx         context.Context
	cancel      context.CancelFunc
	connections []net.Conn
}
type proxyTunnelOwnership struct {
	mu       sync.Mutex
	closed   bool
	tunnels  map[*proxyOwnedTunnel]bool
	handlers sync.WaitGroup
}

func newProxyTunnelOwnership() *proxyTunnelOwnership {
	return &proxyTunnelOwnership{tunnels: make(map[*proxyOwnedTunnel]bool)}
}
func (o *proxyTunnelOwnership) begin(parent context.Context) (*proxyOwnedTunnel, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	tunnel := &proxyOwnedTunnel{ctx: ctx, cancel: cancel}
	o.tunnels[tunnel] = true
	o.handlers.Add(1)
	return tunnel, true
}
func (o *proxyTunnelOwnership) connection(tunnel *proxyOwnedTunnel, connection net.Conn) bool {
	o.mu.Lock()
	if o.closed || !o.tunnels[tunnel] {
		o.mu.Unlock()
		closeProxyConnection(connection)
		return false
	}
	tunnel.connections = append(tunnel.connections, connection)
	o.mu.Unlock()
	return true
}
func (o *proxyTunnelOwnership) finish(tunnel *proxyOwnedTunnel) {
	o.mu.Lock()
	delete(o.tunnels, tunnel)
	connections := append([]net.Conn(nil), tunnel.connections...)
	o.mu.Unlock()
	tunnel.cancel()
	for _, connection := range connections {
		closeProxyConnection(connection)
	}
	o.handlers.Done()
}
func (o *proxyTunnelOwnership) closeAndJoin(timeout time.Duration) bool {
	o.mu.Lock()
	o.closed = true
	var cancellations []context.CancelFunc
	var connections []net.Conn
	for tunnel := range o.tunnels {
		cancellations = append(cancellations, tunnel.cancel)
		connections = append(connections, tunnel.connections...)
	}
	o.mu.Unlock()
	for _, cancel := range cancellations {
		cancel()
	}
	for _, connection := range connections {
		closeProxyConnection(connection)
	}
	done := make(chan struct{})
	go func() { o.handlers.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// An immediate read/write deadline also bounds TLS close_notify; no socket close
// runs under the admission mutex or relies on Server.Close owning hijacked sockets.
func closeProxyConnection(connection net.Conn) {
	if connection == nil {
		return
	}
	_ = connection.SetDeadline(time.Now())
	_ = connection.Close()
}
