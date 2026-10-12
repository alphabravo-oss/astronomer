//go:build unix

package resolver

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// A real private-interface TLS peer reaches the canonical public/private policy
// and its actual dialer. No loopback exception, fake resolver or injected client.
func privateRegistryTLS(t *testing.T, handler http.Handler) (*httptest.Server, []byte, netip.Addr) {
	t.Helper()
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	var address netip.Addr
	for _, entry := range addresses {
		prefix, parseErr := netip.ParsePrefix(entry.String())
		if parseErr == nil && prefix.Addr().Is4() && prefix.Addr().IsPrivate() && !prefix.Addr().IsLoopback() {
			address = prefix.Addr()
			break
		}
	}
	if !address.IsValid() {
		t.Fatal("owned TLS test needs a private interface; no implicit skip or loopback bypass")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(address.String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	rootPublic, rootPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rootTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "owned QA root"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, rootPublic, rootPrivate)
	if err != nil {
		t.Fatal(err)
	}
	rootCertificate, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	leafPublic, leafPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "owned QA registry"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IPAddresses: []net.IP{net.ParseIP(address.String())}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, rootCertificate, leafPublic, rootPrivate)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	_ = server.Listener.Close()
	server.Listener = listener
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER}, PrivateKey: leafPrivate}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}), address
}

func registryTrustFixture(t *testing.T) (DatabaseRegistryTrustOptions, *atomic.Int64, []byte) {
	t.Helper()
	requests := &atomic.Int64{}
	server, roots, address := privateRegistryTLS(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = writer.Write([]byte("owned registry"))
	}))
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "registry.pem"), roots, 0600); err != nil {
		t.Fatal(err)
	}
	return DatabaseRegistryTrustOptions{TrustDirectory: directory, RootsRef: "registry", Network: httpclient.DestinationPolicy{Restricted: true, Origins: []httpclient.TrustedOrigin{{URL: server.URL, CIDRs: []netip.Prefix{netip.PrefixFrom(address, 32)}}}}}, requests, roots
}

func TestDatabaseRegistryTransportPinsActualTLSAndFrozenPolicy(t *testing.T) {
	options, requests, roots := registryTrustFixture(t)
	original := freezeDatabaseRegistryNetwork(options.Network)
	transport, err := CaptureDatabaseRegistryTransport(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	rootFingerprint, policyFingerprint := transport.Fingerprints()
	if rootFingerprint != registryRootFingerprint(roots) || policyFingerprint != databaseRegistryPolicyFingerprint(original) {
		t.Fatal("constructor did not bind exact roots and policy")
	}
	options.Network.Origins[0].CIDRs[0] = netip.MustParsePrefix("10.0.0.0/8")
	options.Network.Origins[0].URL = "https://not-approved.example.test"
	view := transport.Network()
	view.Origins[0].URL = "https://caller-mutated.example.test"
	shell := transport.Client()
	shell.Transport = http.DefaultTransport
	shell.Timeout = time.Millisecond
	client := transport.Client()
	defer client.CloseIdleConnections()
	response, err := client.Get(original.Origins[0].URL)
	if err != nil {
		t.Fatal("actual pinned TLS request failed")
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(body) != "owned registry" || requests.Load() != 1 {
		t.Fatal("actual canonical pinned transport was replaced")
	}
	if _, err := client.Get("https://not-approved.example.test"); err == nil || requests.Load() != 1 {
		t.Fatal("caller mutation granted additional origin")
	}
	if err := os.WriteFile(filepath.Join(options.TrustDirectory, "registry.pem"), append(roots, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(original.Origins[0].URL); err == nil || requests.Load() != 1 {
		t.Fatal("pooled TLS connection ignored exact mounted-byte rotation")
	}
}

func TestDatabaseRegistryTransportRejectsWrongRootsAndConstructorAuthority(t *testing.T) {
	for _, mutation := range []string{"wrong-root", "missing", "symlink", "not-ca", "unrestricted", "HTTP", "legacy-private", "closed", "canceled"} {
		t.Run(mutation, func(t *testing.T) {
			options, requests, _ := registryTrustFixture(t)
			path := filepath.Join(options.TrustDirectory, "registry.pem")
			ctx := t.Context()
			switch mutation {
			case "wrong-root":
				other, _, _ := registryTrustFixture(t)
				data, err := os.ReadFile(filepath.Join(other.TrustDirectory, "registry.pem"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				other, _, _ := registryTrustFixture(t)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(other.TrustDirectory, "registry.pem"), path); err != nil {
					t.Fatal(err)
				}
			case "not-ca":
				if err := os.WriteFile(path, []byte("not a CA certificate"), 0600); err != nil {
					t.Fatal(err)
				}
			case "unrestricted":
				options.Network.Restricted = false
			case "HTTP":
				options.Network.Origins[0].URL = strings.Replace(options.Network.Origins[0].URL, "https:", "http:", 1)
			case "legacy-private":
				options.Network.PrivateHosts = []string{"*"}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			transport, err := CaptureDatabaseRegistryTransport(ctx, options)
			if mutation == "wrong-root" || mutation == "closed" {
				if err != nil {
					t.Fatal(err)
				}
				defer transport.Close()
				client := transport.Client()
				if mutation == "closed" {
					transport.Close()
				}
				if _, err := client.Get(options.Network.Origins[0].URL); err == nil {
					t.Fatal("untrusted or closed registry transport connected")
				}
			} else if err == nil || transport != nil {
				t.Fatal("invalid constructor authority accepted")
			}
			if mutation == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("constructor lost context cancellation")
			}
			if requests.Load() != 0 {
				t.Fatal("rejected trust sent an application request")
			}
		})
	}
}
