package charlie

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type MCPListenerConfig struct {
	Address     string
	Certificate string
	PrivateKey  string
	ClientCA    string
}

// MCPListener is a dedicated private listener. It is never mounted into the
// public Astronomer router and accepts no plaintext fallback.
type MCPListener struct {
	server *http.Server
	addr   string
	reload *certificateReloader
}

func NewMCPListener(config MCPListenerConfig, handler *MCPHandler) (*MCPListener, error) {
	if handler == nil || strings.TrimSpace(config.Address) == "" {
		return nil, fmt.Errorf("Charlie MCP listener requires a private address and handler")
	}
	if config.Certificate == "" || config.PrivateKey == "" || config.ClientCA == "" {
		return nil, fmt.Errorf("Charlie MCP listener requires mounted TLS files")
	}
	reloader, err := newCertificateReloader(config.Certificate, config.PrivateKey)
	if err != nil {
		return nil, err
	}
	caPEM, err := os.ReadFile(config.ClientCA)
	if err != nil {
		return nil, fmt.Errorf("load Charlie MCP client CA: %w", err)
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("Charlie MCP client CA is invalid")
	}
	tlsConfig := &tls.Config{
		MinVersion:     tls.VersionTLS13,
		ClientAuth:     tls.RequireAndVerifyClientCert,
		ClientCAs:      clientCAs,
		GetCertificate: reloader.GetCertificate,
		NextProtos:     []string{"h2", "http/1.1"},
	}
	return &MCPListener{
		addr:   config.Address,
		reload: reloader,
		server: &http.Server{
			Handler: handler, TLSConfig: tlsConfig,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      35 * time.Second,
			IdleTimeout:       60 * time.Second,
			MaxHeaderBytes:    32 << 10,
		},
	}, nil
}

func (l *MCPListener) Serve() error {
	listener, err := net.Listen("tcp", l.addr)
	if err != nil {
		return err
	}
	charlieMCPListenerActive.Set(1)
	defer charlieMCPListenerActive.Set(0)
	tlsListener := tls.NewListener(listener, l.server.TLSConfig)
	return l.server.Serve(tlsListener)
}

func (l *MCPListener) Shutdown(ctx context.Context) error {
	if l == nil || l.server == nil {
		return nil
	}
	return l.server.Shutdown(ctx)
}

type certificateReloader struct {
	certificatePath string
	privateKeyPath  string
	mu              sync.RWMutex
	certificate     *tls.Certificate
	certificateTime time.Time
	privateKeyTime  time.Time
}

func newCertificateReloader(certificatePath, privateKeyPath string) (*certificateReloader, error) {
	r := &certificateReloader{certificatePath: certificatePath, privateKeyPath: privateKeyPath}
	if err := r.reload(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *certificateReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	certificateInfo, certificateErr := os.Stat(r.certificatePath)
	privateKeyInfo, privateKeyErr := os.Stat(r.privateKeyPath)
	if certificateErr != nil || privateKeyErr != nil {
		return nil, fmt.Errorf("Charlie MCP TLS material is unavailable")
	}
	r.mu.RLock()
	changed := !certificateInfo.ModTime().Equal(r.certificateTime) || !privateKeyInfo.ModTime().Equal(r.privateKeyTime)
	r.mu.RUnlock()
	if changed {
		if err := r.reload(); err != nil {
			return nil, err
		}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.certificate, nil
}

func (r *certificateReloader) reload() error {
	certificateInfo, err := os.Stat(r.certificatePath)
	if err != nil {
		return fmt.Errorf("load Charlie MCP server certificate: %w", err)
	}
	privateKeyInfo, err := os.Stat(r.privateKeyPath)
	if err != nil {
		return fmt.Errorf("load Charlie MCP server private key: %w", err)
	}
	pair, err := tls.LoadX509KeyPair(r.certificatePath, r.privateKeyPath)
	if err != nil {
		return fmt.Errorf("parse Charlie MCP server identity: %w", err)
	}
	r.mu.Lock()
	r.certificate = &pair
	r.certificateTime = certificateInfo.ModTime()
	r.privateKeyTime = privateKeyInfo.ModTime()
	r.mu.Unlock()
	return nil
}
