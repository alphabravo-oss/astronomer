package helmruntime

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/helmpath"
	repo "helm.sh/helm/v4/pkg/repo/v1"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

func TestHelmHTTPProxyPreservesNamedVersionRepositoryAuthCAAndTargetIdentity(t *testing.T) {
	fixture := newHelmProxyOperationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, coordinates := range []struct{ name, url, version string }{{"owned/fixture", "", "1.0.0"}, {"fixture", fixture.targetURL, "2.0.0"}} {
		path, err := LocateRepositoryChart(ctx, fixture.runtime, coordinates.name, coordinates.url, coordinates.version)
		if err != nil {
			t.Fatal("owned HTTP CONNECT chart transfer failed")
		}
		chart, err := loader.Load(path)
		if err != nil || chart.Metadata.Version != coordinates.version {
			t.Fatal("proxied named repository version selection changed")
		}
	}
	if fixture.archives.Load() != 2 || fixture.indexes.Load() != 1 || fixture.proxyCalls.Load() != 3 || fixture.targetIdentities.Load() != 3 {
		t.Fatal("proxied repository credential/index/archive/identity counts changed")
	}
}

func TestHelmProxyNoProxyPreservesDirectTransferWithoutGrantingOrigins(t *testing.T) {
	fixture := newHelmProxyOperationFixture(t)
	fixture.runtime.RepositoryProxyRouting.NoProxy = fixture.address
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	path, err := LocateRepositoryChart(ctx, fixture.runtime, "owned/fixture", "", "1.0.0")
	if err != nil {
		t.Fatal("explicit proxy bypass changed private repository transfer")
	}
	chart, err := loader.Load(path)
	if err != nil || chart.Metadata.Version != "1.0.0" || fixture.proxyCalls.Load() != 0 || fixture.targetIdentities.Load() != 1 {
		t.Fatal("explicit NO_PROXY did not preserve direct target identity")
	}
	fixture.runtime.RepositoryDestinationPolicy = fmt.Sprintf(`{"restricted":true,"origins":[{"url":%q,"cidrs":[%q]}]}`, fixture.proxyURL, fixture.cidr)
	getter := repositoryGetter{ctx: ctx, runtime: fixture.runtime, owner: fixture.entry}
	if _, err := getter.Get(fixture.targetURL + "/fixture-2.0.0.tgz"); err == nil || fixture.archives.Load() != 1 || fixture.proxyCalls.Load() != 0 {
		t.Fatal("NO_PROXY granted an unauthorized target origin")
	}
}

func TestHelmProxyAuthorityAndBodyCancellationStayOwnedAndJoined(t *testing.T) {
	fixture := newHelmProxyOperationFixture(t)
	for _, allowed := range []string{fixture.targetURL, fixture.proxyURL} {
		runtime := fixture.runtime
		runtime.RepositoryDestinationPolicy = fmt.Sprintf(`{"restricted":true,"origins":[{"url":%q,"cidrs":[%q]}]}`, allowed, fixture.cidr)
		getter := repositoryGetter{ctx: context.Background(), runtime: runtime, owner: fixture.entry}
		if _, err := getter.Get(fixture.targetURL + "/fixture-1.0.0.tgz"); err == nil || fixture.proxyCalls.Load() != 0 || fixture.archives.Load() != 0 {
			t.Fatal("independent proxy/target authority was bypassed")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	done := make(chan struct{})
	getter := repositoryGetter{ctx: ctx, runtime: fixture.runtime, owner: fixture.entry}
	go func() {
		defer close(done)
		_, err := getter.Get(fixture.targetURL + "/blocked-chart.tgz")
		result <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("owned proxied body caller did not drain")
		}
	})
	select {
	case <-fixture.bodyEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("proxied target response was not established")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("proxied body lost cancellation identity")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proxied body cancellation did not return")
	}
	select {
	case <-fixture.bodyStopped:
	case <-time.After(2 * time.Second):
		t.Fatal("proxied target body handler did not drain")
	}
	if !fixture.tunnels.closeAndJoin(2 * time.Second) {
		t.Fatal("canceled proxy copy workers did not join")
	}
}

type helmProxyOperationFixture struct {
	address, cidr, targetURL, proxyURL              string
	runtime                                         Config
	entry                                           *repo.Entry
	tunnels                                         *proxyTunnelOwnership
	proxyCalls, archives, indexes, targetIdentities atomic.Int32
	bodyEntered, bodyStopped                        chan struct{}
	bodyOnce                                        sync.Once
}

func newHelmProxyOperationFixture(t *testing.T) *helmProxyOperationFixture {
	t.Helper()
	address := os.Getenv("ASTRONOMER_HELM_HTTP_PRIVATE_FIXTURE_IP")
	if address == "" {
		t.Skip("owned private Helm proxy address is not configured")
	}
	ip := net.ParseIP(address)
	if ip == nil || !ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		t.Fatal("owned proxy fixture address is invalid")
	}
	root := t.TempDir()
	serverCertificate, ca, clientPEM, keyPEM := proxyIdentityCertificates(t, ip)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("fixture trust is invalid")
	}
	fixture := &helmProxyOperationFixture{address: address, tunnels: newProxyTunnelOwnership(), bodyEntered: make(chan struct{}), bodyStopped: make(chan struct{})}
	first, second := fixtureChartArchive(t, "1.0.0"), fixtureChartArchive(t, "2.0.0")
	target := privateTLSProxyServer(t, address, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverCertificate}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "repository-user" || password != "fixture-repository-password" || r.Header.Get("Proxy-Authorization") != "" || len(r.TLS.PeerCertificates) != 1 || r.TLS.PeerCertificates[0].Subject.CommonName != "repository-client" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fixture.targetIdentities.Add(1)
		w.Header().Set("Connection", "close")
		switch r.URL.Path {
		case "/index.yaml":
			fixture.indexes.Add(1)
			_, _ = w.Write([]byte(fixtureRepositoryIndex(first, second)))
		case "/fixture-1.0.0.tgz":
			fixture.archives.Add(1)
			_, _ = w.Write(first)
		case "/fixture-2.0.0.tgz":
			fixture.archives.Add(1)
			_, _ = w.Write(second)
		case "/blocked-chart.tgz":
			fixture.bodyOnce.Do(func() { w.(http.Flusher).Flush(); close(fixture.bodyEntered) })
			defer close(fixture.bodyStopped)
			<-r.Context().Done()
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	fixture.targetURL = target.URL
	listener, err := net.Listen("tcp", net.JoinHostPort(address, "0"))
	if err != nil {
		t.Fatal("owned plaintext proxy listener failed")
	}
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.proxyCalls.Add(1)
		expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("proxy-user:fixture-proxy-password"))
		if r.Method != http.MethodConnect || r.Host != target.Listener.Addr().String() || r.Header.Get("Proxy-Authorization") != expected || r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		tunnel, ok := fixture.tunnels.begin(r.Context())
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		defer fixture.tunnels.finish(tunnel)
		upstream, err := (&net.Dialer{Timeout: time.Second}).DialContext(tunnel.ctx, "tcp", target.Listener.Addr().String())
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if !fixture.tunnels.connection(tunnel, upstream) {
			return
		}
		downstream, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			closeProxyConnection(upstream)
			return
		}
		if !fixture.tunnels.connection(tunnel, downstream) {
			return
		}
		defer closeProxyConnection(upstream)
		defer closeProxyConnection(downstream)
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
	proxy.Listener = listener
	proxy.Start()
	t.Cleanup(proxy.Close)
	fixture.proxyURL = proxy.URL
	t.Cleanup(func() {
		if !fixture.tunnels.closeAndJoin(4 * time.Second) {
			t.Error("owned proxy operation workers did not drain")
		}
	})
	caFile, certFile, keyFile := filepath.Join(root, "ca.pem"), filepath.Join(root, "client.pem"), filepath.Join(root, "client-key.pem")
	for path, value := range map[string][]byte{caFile: ca, certFile: clientPEM, keyFile: keyPEM} {
		if err := os.WriteFile(path, value, 0o600); err != nil {
			t.Fatal("owned proxy trust file creation failed")
		}
	}
	bits := 128
	if ip.To4() != nil {
		bits = 32
	}
	fixture.cidr = fmt.Sprintf("%s/%d", address, bits)
	proxyAddress, err := http.NewRequest(http.MethodGet, proxy.URL, nil)
	if err != nil {
		t.Fatal("owned proxy address is invalid")
	}
	proxyAddress.URL.User = url.UserPassword("proxy-user", "fixture-proxy-password")
	fixture.runtime = Config{RepositoryConfig: filepath.Join(root, "repositories.yaml"), RepositoryCache: filepath.Join(root, "repository"), ContentCache: filepath.Join(root, "content"), RepositoryDestinationPolicy: fmt.Sprintf(`{"origins":[{"url":%q,"cidrs":[%q]},{"url":%q,"cidrs":[%q]}]}`, target.URL, fixture.cidr, proxy.URL, fixture.cidr), RepositoryProxyRouting: httpclient.ProxyRouting{URL: proxyAddress.URL.String()}}
	fixture.entry = &repo.Entry{Name: "owned", URL: target.URL, Username: "repository-user", Password: "fixture-repository-password", CAFile: caFile, CertFile: certFile, KeyFile: keyFile}
	file := repo.NewFile()
	file.Add(fixture.entry)
	if err := file.WriteFile(fixture.runtime.RepositoryConfig, 0o600); err != nil {
		t.Fatal("owned repository file creation failed")
	}
	if err := os.MkdirAll(fixture.runtime.RepositoryCache, 0o700); err != nil {
		t.Fatal("owned repository cache creation failed")
	}
	if err := os.WriteFile(filepath.Join(fixture.runtime.RepositoryCache, helmpath.CacheIndexFile(fixture.entry.Name)), []byte(fixtureRepositoryIndex(first, second)), 0o600); err != nil {
		t.Fatal("owned index cache write failed")
	}
	return fixture
}
