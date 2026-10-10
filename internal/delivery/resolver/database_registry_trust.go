package resolver

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// DatabaseRegistryTrustOptions is operator constructor authority. Artifact,
// feed and catalog fields cannot select roots, origins, redirects or private
// destinations. This optional DB path uses explicit roots only; ordinary
// distribution loading retains its existing system-root behavior.
type DatabaseRegistryTrustOptions struct {
	TrustDirectory string
	RootsRef       string
	Network        httpclient.DestinationPolicy
}

// DatabaseRegistryTransport owns the canonical client and exact captured root
// bytes. Its pool and transport are never exposed. Client returns an independent
// client shell around a private forwarding transport, not TLS configuration.
type DatabaseRegistryTransport struct {
	mutex                               sync.RWMutex
	mounted                             *ExecVerifier
	reference                           string
	network                             httpclient.DestinationPolicy
	client                              *http.Client
	rootsFingerprint, policyFingerprint string
	mountOriginFingerprint              string
	closed                              bool
}

func CaptureDatabaseRegistryTransport(ctx context.Context, options DatabaseRegistryTrustOptions) (*DatabaseRegistryTransport, error) {
	if ctx == nil || !strictTrustOpeningAvailable || !options.Network.Restricted || options.Network.Validate() != nil || len(options.Network.PrivateHosts) != 0 || len(options.Network.PrivateCIDRs) != 0 {
		return nil, errors.New("database registry trust constructor invalid")
	}
	for _, origin := range options.Network.Origins {
		parsed, err := url.Parse(origin.URL)
		if err != nil || parsed.Scheme != "https" {
			return nil, errors.New("database registry requires bounded HTTPS origins")
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	verifier, err := NewExecVerifier(options.TrustDirectory)
	if err != nil {
		return nil, errors.New("database registry roots unavailable")
	}
	material, _, err := verifier.readTrustKeyWithHook(ctx, options.RootsRef, ".pem", nil)
	if err != nil {
		return nil, err
	}
	defer clearBytes(material)
	pool, err := httpclient.ParseCertificateAuthorities(material)
	if err != nil {
		return nil, err
	}
	policy := freezeDatabaseRegistryNetwork(options.Network)
	client, err := httpclient.NewDestinationClient(policy, httpclient.DestinationOptions{
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool},
		Timeout:   30 * time.Second, MaxResponseBytes: 128 << 20,
	})
	if err != nil {
		return nil, err
	}
	transport := &DatabaseRegistryTransport{mounted: verifier, reference: options.RootsRef,
		network: policy, client: client, rootsFingerprint: registryRootFingerprint(material),
		policyFingerprint:      databaseRegistryPolicyFingerprint(policy),
		mountOriginFingerprint: registryTransportFingerprint("astronomer-database-registry-root-origin-v1", verifier.trustDir, options.RootsRef)}
	if err := transport.Revalidate(ctx); err != nil {
		transport.Close()
		return nil, err
	}
	return transport, nil
}

func freezeDatabaseRegistryNetwork(policy httpclient.DestinationPolicy) httpclient.DestinationPolicy {
	policy.Origins = append([]httpclient.TrustedOrigin(nil), policy.Origins...)
	for index := range policy.Origins {
		policy.Origins[index].CIDRs = append([]netip.Prefix(nil), policy.Origins[index].CIDRs...)
	}
	return policy
}

func registryRootFingerprint(material []byte) string {
	digest := sha256.Sum256(material)
	return fmt.Sprintf("sha256:%x", digest)
}

func databaseRegistryPolicyFingerprint(policy httpclient.DestinationPolicy) string {
	// Exact constructor inputs and fixed client semantics are framed, sorted and
	// hashed without JSON serialization. No artifact can add transport authority.
	fields := []string{"astronomer-database-registry-transport-v1", "restricted", "HTTPS",
		"pinned-roots-only", "TLS>=1.2", "timeout=30s", "body=134217728", "same-origin-redirects=3", "no-proxy"}
	origins := append([]httpclient.TrustedOrigin(nil), policy.Origins...)
	sort.Slice(origins, func(i, j int) bool { return origins[i].URL < origins[j].URL })
	for _, origin := range origins {
		fields = append(fields, "origin", origin.URL)
		prefixes := make([]string, 0, len(origin.CIDRs))
		for _, prefix := range origin.CIDRs {
			prefixes = append(prefixes, prefix.String())
		}
		sort.Strings(prefixes)
		fields = append(fields, fmt.Sprintf("CIDRs=%d", len(prefixes)))
		fields = append(fields, prefixes...)
	}
	return registryTransportFingerprint(fields...)
}

func registryTransportFingerprint(fields ...string) string {
	hash := sha256.New()
	for _, field := range fields {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(field))
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil))
}

func (transport *DatabaseRegistryTransport) Revalidate(ctx context.Context) error {
	if transport == nil || ctx == nil {
		return errors.New("database registry transport unavailable")
	}
	transport.mutex.RLock()
	defer transport.mutex.RUnlock()
	if transport.closed {
		return errors.New("database registry transport unavailable")
	}
	material, _, err := transport.mounted.readTrustKeyWithHook(ctx, transport.reference, ".pem", nil)
	if err != nil {
		return err
	}
	defer clearBytes(material)
	if registryRootFingerprint(material) != transport.rootsFingerprint {
		return errors.New("database registry mounted roots changed")
	}
	return ctx.Err()
}

func (transport *DatabaseRegistryTransport) Fingerprints() (roots, policy string) {
	if transport == nil {
		return "", ""
	}
	transport.mutex.RLock()
	defer transport.mutex.RUnlock()
	if transport.closed {
		return "", ""
	}
	return transport.rootsFingerprint, transport.policyFingerprint
}

func (transport *DatabaseRegistryTransport) RootOriginFingerprint() string {
	if transport == nil {
		return ""
	}
	transport.mutex.RLock()
	defer transport.mutex.RUnlock()
	if transport.closed {
		return ""
	}
	return transport.mountOriginFingerprint
}

func (transport *DatabaseRegistryTransport) Network() httpclient.DestinationPolicy {
	if transport == nil {
		return httpclient.DestinationPolicy{}
	}
	transport.mutex.RLock()
	defer transport.mutex.RUnlock()
	if transport.closed {
		return httpclient.DestinationPolicy{}
	}
	return freezeDatabaseRegistryNetwork(transport.network)
}

// Client never exposes the owned client shell or concrete TLS transport. Mutating
// a returned shell does not change the frozen constructor's pool or policy.
func (transport *DatabaseRegistryTransport) Client() *http.Client {
	if transport == nil {
		return nil
	}
	transport.mutex.RLock()
	defer transport.mutex.RUnlock()
	if transport.closed {
		return nil
	}
	client := *transport.client
	client.Transport = databaseRegistryRoundTripper{owner: transport}
	return &client
}

type databaseRegistryRoundTripper struct{ owner *DatabaseRegistryTransport }

func (round databaseRegistryRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil {
		return nil, errors.New("database registry request invalid")
	}
	if err := round.owner.Revalidate(request.Context()); err != nil {
		return nil, err
	}
	round.owner.mutex.RLock()
	defer round.owner.mutex.RUnlock()
	if round.owner.closed {
		return nil, errors.New("database registry transport unavailable")
	}
	return round.owner.client.Transport.RoundTrip(request)
}

func (round databaseRegistryRoundTripper) CloseIdleConnections() { round.owner.closeIdleConnections() }

func (transport *DatabaseRegistryTransport) closeIdleConnections() {
	transport.mutex.RLock()
	defer transport.mutex.RUnlock()
	transport.client.CloseIdleConnections()
}

func (transport *DatabaseRegistryTransport) Close() {
	if transport == nil {
		return
	}
	transport.mutex.Lock()
	defer transport.mutex.Unlock()
	if !transport.closed {
		transport.closed = true
		transport.client.CloseIdleConnections()
	}
}
