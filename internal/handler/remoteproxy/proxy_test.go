package remoteproxy_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rancher/remotedialer"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/alphabravocompany/astronomer-go/internal/handler/remoteproxy"
)

// fakeTunnel implements remoteproxy.TunnelDialer in-memory: every dial is
// short-circuited to a local httptest.Server that pretends to be the
// in-cluster API server. This is the smallest possible end-to-end test of
// the K8sClient plumbing — it does NOT run the remotedialer protocol, but it
// does exercise rest.Config -> http.Transport -> Dial -> response parsing.
type fakeTunnel struct {
	target  string // host:port of the httptest.Server
	hasSess bool
}

func (f *fakeTunnel) HasSession(_ string) bool { return f.hasSess }

func (f *fakeTunnel) DialerFor(_ string) remotedialer.Dialer {
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, f.target)
	}
}

// podListHandler answers GET .../pods with a minimal one-item PodList.
func podListHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/pods") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kind":       "PodList",
			"apiVersion": "v1",
			"metadata":   map[string]any{},
			"items": []map[string]any{
				{
					"metadata": map[string]any{"name": "demo-pod", "namespace": "default"},
					"status":   map[string]any{"phase": "Running"},
				},
			},
		})
	}
}

// newAPIServer builds an httptest TLS server whose leaf certificate carries
// the supplied SANs, signed by a freshly minted CA. It returns the server, the
// CA bundle (PEM) that verifies it, and the host:port for net.Dial. The k8s
// rest.Config derives its TLS ServerName from the Host
// ("kubernetes.default.svc"), so the success path needs that DNS SAN.
func newAPIServer(t *testing.T, dnsNames ...string) (srv *httptest.Server, caPEM []byte, target string) {
	t.Helper()

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("ca key: %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "remoteproxy-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("ca cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse ca: %v", err)
	}

	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("leaf key: %v", err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "apiserver"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("leaf cert: %v", err)
	}

	srv = httptest.NewUnstartedServer(podListHandler())
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{leafDER},
			PrivateKey:  leafKey,
		}},
		MinVersion: tls.VersionTLS12,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	target = strings.TrimPrefix(srv.URL, "https://")
	return srv, caPEM, target
}

func TestK8sClient_VerifiesAPIServerCert(t *testing.T) {
	// Leaf cert carries the SAN the rest.Config verifies against
	// (Host -> ServerName "kubernetes.default.svc").
	_, caPEM, target := newAPIServer(t, "kubernetes.default.svc")

	tunnel := &fakeTunnel{target: target, hasSess: true}

	client, err := remoteproxy.K8sClientWithOptions(tunnel, "00000000-0000-0000-0000-000000000001", remoteproxy.TLSOptions{CAPEM: caPEM})
	if err != nil {
		t.Fatalf("K8sClient: %v", err)
	}

	pods, err := client.CoreV1().Pods("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(pods.Items) != 1 || pods.Items[0].Name != "demo-pod" {
		t.Fatalf("unexpected pod list: %+v", pods.Items)
	}
}

// TestK8sClient_RejectsBadAPIServerCert is the negative test mandated by H1t:
// when the apiserver presents a cert that does NOT chain to the pinned CA, the
// secure v2 transport refuses the connection.
func TestK8sClient_RejectsBadAPIServerCert(t *testing.T) {
	// Server is signed by CA-A; we pin a *different* CA-B.
	_, _, target := newAPIServer(t, "kubernetes.default.svc")
	_, otherCAPEM, _ := newAPIServer(t, "kubernetes.default.svc")

	tunnel := &fakeTunnel{target: target, hasSess: true}

	client, err := remoteproxy.K8sClientWithOptions(tunnel, "00000000-0000-0000-0000-000000000001", remoteproxy.TLSOptions{CAPEM: otherCAPEM})
	if err != nil {
		t.Fatalf("K8sClient build: %v", err)
	}

	_, err = client.CoreV1().Pods("default").List(context.Background(), metav1.ListOptions{})
	if err == nil {
		t.Fatal("expected TLS verification to reject the mismatched apiserver cert, got nil error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "certificate") &&
		!strings.Contains(strings.ToLower(err.Error()), "x509") &&
		!strings.Contains(strings.ToLower(err.Error()), "authority") {
		t.Fatalf("expected a certificate-verification error, got: %v", err)
	}
}

// TestK8sClient_AlsoRejectsHostnameMismatch proves the SAN/hostname is
// verified too: a cert signed by the pinned CA but for the wrong host is
// rejected.
func TestK8sClient_AlsoRejectsHostnameMismatch(t *testing.T) {
	_, caPEM, target := newAPIServer(t, "wrong.example.com")

	tunnel := &fakeTunnel{target: target, hasSess: true}

	client, err := remoteproxy.K8sClientWithOptions(tunnel, "00000000-0000-0000-0000-000000000001", remoteproxy.TLSOptions{CAPEM: caPEM})
	if err != nil {
		t.Fatalf("K8sClient build: %v", err)
	}
	if _, err := client.CoreV1().Pods("default").List(context.Background(), metav1.ListOptions{}); err == nil {
		t.Fatal("expected hostname verification to reject cert for wrong.example.com")
	}
}

// TestTLSOptions_ProductionCannotEnableInsecure guards the core invariant of
// H1t: production config can never select the InsecureSkipVerify escape hatch.
func TestTLSOptions_ProductionCannotEnableInsecure(t *testing.T) {
	tunnel := &fakeTunnel{target: "127.0.0.1:1", hasSess: true}

	if _, err := remoteproxy.RestConfigWithOptions(tunnel, "abc", remoteproxy.TLSOptions{Insecure: true, Production: true}); err == nil {
		t.Fatal("production + Insecure must be refused")
	}
	if _, err := remoteproxy.K8sClientWithOptions(tunnel, "abc", remoteproxy.TLSOptions{Insecure: true, Production: true}); err == nil {
		t.Fatal("production + Insecure must be refused via K8sClientWithOptions")
	}

	// Non-production may opt in (loudly logged).
	if _, err := remoteproxy.RestConfigWithOptions(tunnel, "abc", remoteproxy.TLSOptions{Insecure: true, Production: false}); err != nil {
		t.Fatalf("non-prod insecure opt-in should succeed: %v", err)
	}
}

// TestRestConfig_MissingCAIsAnError proves the secure default fails closed:
// with no CA bundle available and no insecure opt-in, building the config
// errors rather than silently skipping verification.
func TestRestConfig_MissingCAIsAnError(t *testing.T) {
	tunnel := &fakeTunnel{target: "127.0.0.1:1", hasSess: true}

	if _, err := remoteproxy.RestConfigWithOptions(tunnel, "abc", remoteproxy.TLSOptions{
		CAPath: filepath.Join(t.TempDir(), "does-not-exist-ca.crt"),
	}); err == nil {
		t.Fatal("missing CA bundle with no insecure opt-in must be an error")
	}
}

func TestK8sClient_AgentOffline(t *testing.T) {
	tunnel := &fakeTunnel{hasSess: false}
	if _, err := remoteproxy.K8sClient(tunnel, "abc"); err == nil {
		t.Fatal("expected error when agent has no session")
	}
}

func TestK8sClient_NilServer(t *testing.T) {
	if _, err := remoteproxy.K8sClient(nil, "abc"); err == nil {
		t.Fatal("expected error for nil tunnel")
	}
}

func TestK8sClient_EmptyClusterID(t *testing.T) {
	tunnel := &fakeTunnel{hasSess: true}
	if _, err := remoteproxy.K8sClient(tunnel, ""); err == nil {
		t.Fatal("expected error for empty cluster id")
	}
}
