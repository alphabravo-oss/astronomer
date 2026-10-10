//go:build linux

package upgradecli

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

// This runs the real factory, real offline signatures, canonical guarded
// repository client and real joined synthetic children. The declared private
// proxy maps exactly ghcr.io:443 to an owned TLS listener; GHCR public DNS is
// still independently checked by canonical destination admission. It is not a
// hermetic DNS test, real publisher/registry/official CLI or live K8s/PG claim.
func TestPrepareInvocationActualCryptoAndCanonicalProxyComposesOneCapturedDriver(t *testing.T) {
	address := os.Getenv("ASTRONOMER_HELM_HTTP_PRIVATE_FIXTURE_IP")
	if address == "" {
		t.Skip("owned private canonical-proxy fixture address is not configured")
	}
	ip := net.ParseIP(address)
	if ip == nil || !ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		t.Fatal("owned proxy address invalid")
	}
	fixture := newLocalChartCryptoFixture(t, false, "17")
	policy, repositoryOptions := invocationCanonicalProxy(t, ip, fixture.chart.client)
	options := invocationActualChildren(t, fixture.metadata, policy, repositoryOptions)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	invocation, err := PrepareInvocation(ctx, options)
	if err != nil {
		t.Fatal("actual positive factory preparation failed", err)
	}
	t.Cleanup(func() {
		if err := invocation.Close(); err != nil {
			t.Error("owned factory cleanup failed", err)
		}
	})
	driver := invocation.driver
	if driver == nil || driver.verification.chart != invocation.chart || driver.backup.chart != invocation.chart || driver.target.database.source != driver.backup.source || driver.target.observation.source != driver.backup.source || driver.verification.commands != driver.release.fence.commands {
		t.Fatal("prepared concrete Driver did not preserve shared chart/command/database owners")
	}
	if driver.backup.attempted || driver.target.database.anchor != nil || driver.target.database.capturing {
		t.Fatal("factory preparation performed backup/database mutation")
	}
	if err := driver.bindChartArguments(ctx); err != nil {
		t.Fatal("authenticated runtime/chart arguments differ", err)
	}
	if err := invocation.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"release-manifest.json", "release-manifest.sigstore.json", "astronomer-1.2.2.tgz", "RELEASE_IMAGES", "SHA256SUMS"} {
		if _, err := os.Stat(filepath.Join(fixture.metadata.directory, name)); err != nil {
			t.Fatal("factory Close deleted private originals", err)
		}
	}
	if _, err := os.Stat(filepath.Join(options.CheckpointDirectory, "checkpoint.lock")); err != nil {
		t.Fatal("private checkpoint lock material removed", err)
	}
	if invocation.Run(ctx).ExitCode == 0 {
		t.Fatal("closed factory replayed")
	}
}

func invocationActualChildren(t *testing.T, fixture metadataCryptoFixture, policy httpclient.DestinationPolicy, repositoryOptions httpclient.DestinationOptions) InvocationOptions {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	captures, checkpoint := filepath.Join(root, "captures"), filepath.Join(root, "checkpoint")
	for _, path := range []string{captures, checkpoint} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(root, "kubeconfig")
	if err := os.WriteFile(config, []byte("synthetic owned kubeconfig; child supplies typed responses"), 0600); err != nil {
		t.Fatal(err)
	}
	writeTool := func(kind ToolKind, version, script string) Tool {
		path := filepath.Join(root, "owned-synthetic-"+string(kind))
		if err := os.WriteFile(path, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(script))
		return Tool{Kind: kind, Path: path, Version: version, SHA256: "sha256:" + hex.EncodeToString(sum[:])}
	}
	helm := writeTool(Helm, "v4.3.0", "#!/bin/sh\nif [ \"$1\" = version ]; then printf v4.3.0; exit 0; fi\nexit 90\n")
	kubectl := writeTool(Kubectl, "v1.37.1", `#!/bin/sh
if [ "$1" = version ]; then printf 'Client Version: v1.37.1\nKustomize Version: v5.7.1\n'; exit 0; fi
if [ "$1" != --kubeconfig ] || [ "$3" != --context ] || [ "$4" != fixture-context ]; then exit 91; fi
case "$5 $6" in
'config view') printf https://kubernetes.example.test;;
'get namespace') [ "$7" = astronomer ] || exit 92; printf 'namespace-original\tastronomer\tlive';;
'get secret') [ "$7" = sh.helm.release.v1.astronomer.v1 ] || exit 93; printf 'release-original\tsh.helm.release.v1.astronomer.v1\tastronomer\thelm.sh/release.v1\thelm\tastronomer\t1\tlive';;
*) exit 94;;
esac
`)
	github := writeTool(GitHubCLI, "v2.87.0", "#!/bin/sh\nif [ \"$1\" = version ]; then printf 'gh version 2.87.0\\nhttps://github.com/cli/cli/releases/tag/v2.87.0\\n'; exit 0; fi\nexit 95\n")
	manifest, err := decodeAuthenticatedManifest(fixture.manifest, fixture.target)
	if err != nil {
		t.Fatal(err)
	}
	var references []string
	for _, image := range manifest.Astronomer.Images {
		references = append(references, image.Reference)
	}
	sort.Strings(references)
	fixture.write(t, "RELEASE_IMAGES", []byte(strings.Join(references, "\n")+"\n"))
	var checksums []string
	for _, name := range []string{"RELEASE_IMAGES", "astronomer-1.2.2.tgz", "release-manifest.json", "release-manifest.sigstore.json"} {
		raw, err := os.ReadFile(filepath.Join(fixture.directory, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		checksums = append(checksums, hex.EncodeToString(sum[:])+"  "+name)
	}
	fixture.write(t, "SHA256SUMS", []byte(strings.Join(checksums, "\n")+"\n"))
	identity := upgrade.Identity{Context: "fixture-context", APIServer: "https://kubernetes.example.test", Namespace: "astronomer", NamespaceUID: "namespace-original", Release: "astronomer", ReleaseUID: "release-original"}
	plan := upgrade.Plan{Target: fixture.target, Previous: "v1.2.1", Mode: upgrade.Preview, Identity: identity, Timeout: 30 * time.Second, CleanupTimeout: 2 * time.Second}
	return InvocationOptions{Plan: plan, Commands: CommandOptions{Directory: root, CaptureDirectory: captures, Timeout: 2 * time.Second, TerminationGrace: 100 * time.Millisecond, MaximumCaptureBytes: 16 << 10}, Helm: helm, Kubectl: kubectl, GH: github, Fence: FenceOptions{Identity: identity, Kubeconfig: config, Revision: 1, DatabasePod: "postgres-0"}, Database: DatabaseBackupOptions{Database: "astronomer", RestoreDatabase: "astronomer_upgrade_verify_fixture", DatabasePodUID: "database-original", Owner: "synthetic-factory", ExpectedMajor: 17, ArtifactPolicy: ArtifactPolicy{MaximumBytes: 1 << 20}}, ReleaseDirectory: fixture.directory, TrustDirectory: fixture.trustDirectory, CheckpointDirectory: checkpoint, ContentPolicy: ArtifactPolicy{MaximumBytes: 1 << 20}, RecoveryPolicy: ArtifactPolicy{MaximumBytes: 1 << 20}, MinimumReadyNodes: 1, RepositoryPolicy: policy, RepositoryOptions: repositoryOptions}
}

func invocationCanonicalProxy(t *testing.T, ip net.IP, localOCI *http.Client) (httpclient.DestinationPolicy, httpclient.DestinationOptions) {
	t.Helper()
	certificate, roots := invocationProxyTLS(t, ip)
	startServer := func(handler http.Handler) *httptest.Server {
		listener, err := net.Listen("tcp", net.JoinHostPort(ip.String(), "0"))
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewUnstartedServer(handler)
		server.Listener = listener
		server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
		server.StartTLS()
		t.Cleanup(server.Close)
		return server
	}
	target := startServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "ghcr.io" || r.Method != http.MethodGet && r.Method != http.MethodHead || r.URL.RawQuery != "" {
			http.Error(w, "outside owned target", http.StatusForbidden)
			return
		}
		request, err := http.NewRequestWithContext(r.Context(), r.Method, "https://ghcr.io"+r.URL.EscapedPath(), nil)
		if err != nil {
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		response, err := localOCI.Do(request)
		if err != nil {
			http.Error(w, "owned OCI upstream failed", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.Header().Set("Connection", "close")
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(response.Body, 1<<20))
	}))
	owner := &invocationTunnelOwner{connections: make(map[net.Conn]bool)}
	proxy := startServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "ghcr.io:443" || !owner.begin() {
			http.Error(w, "outside owned CONNECT", http.StatusForbidden)
			return
		}
		defer owner.done.Done()
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		upstream, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", target.Listener.Addr().String())
		if err != nil {
			http.Error(w, "owned target unavailable", http.StatusBadGateway)
			return
		}
		if !owner.add(upstream) {
			return
		}
		defer owner.remove(upstream)
		downstream, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		if !owner.add(downstream) {
			return
		}
		defer owner.remove(downstream)
		if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if buffered.Flush() != nil {
			return
		}
		_ = upstream.SetDeadline(time.Now().Add(5 * time.Second))
		_ = downstream.SetDeadline(time.Now().Add(5 * time.Second))
		copied := make(chan struct{})
		go func() {
			_, _ = io.Copy(upstream, buffered)
			_ = upstream.Close()
			_ = downstream.Close()
			close(copied)
		}()
		_, _ = io.Copy(downstream, upstream)
		_ = upstream.Close()
		_ = downstream.Close()
		<-copied
	}))
	// Registered before the first request, after both server cleanup callbacks:
	// no fatal/setup/positive path leaves a hijacked socket or copy worker behind.
	t.Cleanup(func() {
		if !owner.closeAndJoin() {
			t.Error("owned factory proxy handlers/copy workers did not join")
		}
	})
	address := netip.MustParseAddr(ip.String()).Unmap()
	bits := 128
	if address.Is4() {
		bits = 32
	}
	policy := httpclient.DestinationPolicy{Origins: []httpclient.TrustedOrigin{{URL: proxy.URL, CIDRs: []netip.Prefix{netip.PrefixFrom(address, bits)}}}}
	options := httpclient.DestinationOptions{Timeout: 5 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, ProxyRouting: &httpclient.ProxyRouting{URL: proxy.URL}}
	return policy, options
}

type invocationTunnelOwner struct {
	mu          sync.Mutex
	closed      bool
	connections map[net.Conn]bool
	done        sync.WaitGroup
}

func (o *invocationTunnelOwner) begin() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return false
	}
	o.done.Add(1)
	return true
}
func (o *invocationTunnelOwner) add(connection net.Conn) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		_ = connection.Close()
		return false
	}
	o.connections[connection] = true
	return true
}
func (o *invocationTunnelOwner) remove(connection net.Conn) {
	_ = connection.Close()
	o.mu.Lock()
	delete(o.connections, connection)
	o.mu.Unlock()
}
func (o *invocationTunnelOwner) closeAndJoin() bool {
	o.mu.Lock()
	o.closed = true
	for connection := range o.connections {
		_ = connection.Close()
	}
	o.mu.Unlock()
	done := make(chan struct{})
	go func() { o.done.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(6 * time.Second):
		return false
	}
}

func invocationProxyTLS(t *testing.T, ip net.IP) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	profile := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic-owned-factory-proxy"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), DNSNames: []string{"ghcr.io"}, IPAddresses: []net.IP{ip}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	raw, err := x509.CreateCertificate(rand.Reader, profile, profile, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{raw}, PrivateKey: key}, roots
}
