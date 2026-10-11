//go:build unix && integration

package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/config"
)

// The test owns a real HTTPS/WebSocket frontend across both joined NewApp
// phases. The private certificate key is ROOT-supplied listener material, never
// a distribution signing key. The proxy cannot choose an upstream: every
// request goes to one captured loopback backend, with caller forwarding headers
// replaced. The ordinary product trusted-proxy parser must explicitly trust
// only that loopback socket. No insecure agent option or fake tunnel is used.
func localQAStartOwnedTLSFrontend(t *testing.T, ctx context.Context, cfg *config.Config, input localQARuntimeInput, backendAddress string) func() {
	t.Helper()
	external, err := url.Parse(cfg.ServerURL)
	if err != nil || external.Scheme != "https" || external.User != nil || external.Hostname() == "" || external.Path != "" || external.RawQuery != "" || external.Fragment != "" || external.ForceQuery || cfg.TrustedProxyCIDRs != "127.0.0.1/32" {
		t.Fatal("explicit owned HTTPS origin/loopback proxy trust required")
	}
	address, err := netip.ParseAddrPort(input.ServerPinnedAddress)
	if err != nil || address.Addr().IsUnspecified() || address.Addr().IsMulticast() || address.Port() == 0 {
		t.Fatal("owned HTTPS listener address invalid")
	}
	port := external.Port()
	if port == "" {
		port = "443"
	}
	_, boundPort, err := net.SplitHostPort(address.String())
	if err != nil || port != boundPort {
		t.Fatal("owned HTTPS listener differs from captured origin port")
	}
	ca := localQAReadPrivateInput(t, input.ServerCAFile, 256<<10)
	defer clear(ca)
	public := localQAReadPrivateInput(t, input.ServerCertificateFile, 256<<10)
	defer clear(public)
	private := localQAReadPrivateInput(t, input.ServerKeyFile, 256<<10)
	defer clear(private)
	certificate, err := tls.X509KeyPair(public, private)
	if err != nil || len(certificate.Certificate) == 0 {
		t.Fatal("owned HTTPS listener key/certificate invalid")
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		t.Fatal("owned HTTPS listener leaf invalid")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("owned HTTPS listener roots invalid")
	}
	intermediates := x509.NewCertPool()
	for _, encoded := range certificate.Certificate[1:] {
		item, e := x509.ParseCertificate(encoded)
		if e != nil {
			t.Fatal("owned HTTPS listener chain invalid")
		}
		intermediates.AddCert(item)
	}
	if _, err = leaf.Verify(x509.VerifyOptions{DNSName: external.Hostname(), Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Fatal("owned HTTPS listener exact hostname/root verification failed")
	}
	backend, err := url.Parse("http://" + backendAddress)
	if err != nil || backend.Hostname() != "127.0.0.1" {
		t.Fatal("owned private backend differs")
	}
	transport := &http.Transport{Proxy: nil, DisableCompression: true, MaxIdleConnsPerHost: 8, ResponseHeaderTimeout: 30 * time.Second}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != backendAddress {
			return nil, http.ErrNotSupported
		}
		return dialer.DialContext(ctx, "tcp", backendAddress)
	}
	proxy := &httputil.ReverseProxy{ErrorLog: log.New(io.Discard, "", 0), Transport: transport, Rewrite: func(request *httputil.ProxyRequest) {
		request.SetURL(backend)
		request.Out.Host = external.Host
		request.Out.Header.Del("Forwarded")
		request.Out.Header.Del("X-Forwarded-For")
		request.Out.Header.Del("X-Forwarded-Host")
		request.Out.Header.Del("X-Forwarded-Proto")
		request.Out.Header.Set("X-Forwarded-Proto", "https")
		request.Out.Header.Set("X-Forwarded-Host", external.Host)
	}, ErrorHandler: func(writer http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(writer, "owned QA backend unavailable", http.StatusBadGateway)
	}}
	listener, err := net.Listen("tcp", address.String())
	if err != nil {
		transport.CloseIdleConnections()
		t.Fatal("owned HTTPS listener bind failed")
	}
	var mutex sync.Mutex
	connections := map[net.Conn]bool{}
	var handlers sync.WaitGroup
	server := &http.Server{ErrorLog: log.New(io.Discard, "", 0), Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlers.Add(1)
		defer handlers.Done()
		if !strings.EqualFold(r.Host, external.Host) {
			http.Error(w, "owned QA origin differs", http.StatusMisdirectedRequest)
			return
		}
		proxy.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10, BaseContext: func(net.Listener) context.Context { return ctx }, ConnState: func(conn net.Conn, state http.ConnState) {
		mutex.Lock()
		defer mutex.Unlock()
		if state == http.StateClosed {
			delete(connections, conn)
		} else {
			connections[conn] = true
		}
	}, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}}
	result := make(chan error, 1)
	go func() { result <- server.Serve(tls.NewListener(listener, server.TLSConfig)) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			bounded, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			shutdownErr := server.Shutdown(bounded)
			_ = server.Close()
			mutex.Lock()
			owned := make([]net.Conn, 0, len(connections))
			for connection := range connections {
				owned = append(owned, connection)
				delete(connections, connection)
			}
			mutex.Unlock()
			for _, connection := range owned {
				_ = connection.Close()
			}
			transport.CloseIdleConnections()
			select {
			case e := <-result:
				if e != nil && e != http.ErrServerClosed {
					t.Error("owned HTTPS frontend listener failed")
				}
			case <-time.After(5 * time.Second):
				t.Error("owned HTTPS frontend listener unjoined; process must fail/drain")
			}
			handlersJoined := make(chan struct{})
			go func() { handlers.Wait(); close(handlersJoined) }()
			select {
			case <-handlersJoined:
			case <-time.After(5 * time.Second):
				t.Error("owned HTTPS frontend handlers unjoined; process must fail/drain")
			}
			if shutdownErr != nil {
				t.Error("owned HTTPS frontend graceful shutdown failed")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}
