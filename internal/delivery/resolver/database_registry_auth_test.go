package resolver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// This owned TLS peer characterizes real HTTP challenge/token/range mechanics.
// Its explicit test transport maps logical HTTPS origins to the peer; it does
// not qualify supplier DNS, operator destination policy or production trust.
type databaseAuthenticationPeer struct {
	mutex         sync.Mutex
	scenario      string
	layer         ocispec.Descriptor
	private       bool
	registryReads int
	tokenReads    int
	ranges        []string
	violations    bool
}

func (peer *databaseAuthenticationPeer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	peer.mutex.Lock()
	defer peer.mutex.Unlock()
	if request.URL.Path == "/token" {
		peer.tokenReads++
		query := request.URL.Query()
		user, password, basic := request.BasicAuth()
		if request.Method != http.MethodGet || query.Get("service") != "registry.test" || query.Get("scope") != "repository:supplier/db:pull" || len(query) != 2 || request.Header.Get("Cookie") != "" || peer.private && (!basic || user != "qa-user" || password != "qa-password") || !peer.private && request.Header.Get("Authorization") != "" {
			peer.violations = true
			writer.WriteHeader(http.StatusForbidden)
			return
		}
		switch peer.scenario {
		case "token-redirect":
			http.Redirect(writer, request, "https://token.test/changed", http.StatusTemporaryRedirect)
		case "token-duplicate":
			_, _ = io.WriteString(writer, `{"token":"qa-token","token":"other"}`)
		case "token-oversized":
			_, _ = io.WriteString(writer, `{"token":"`+strings.Repeat("q", 128<<10)+`"}`)
		case "token-conflicting":
			_, _ = io.WriteString(writer, `{"token":"qa-token","access_token":"different"}`)
		case "token-trailing":
			_, _ = io.WriteString(writer, `{"token":"qa-token"} {}`)
		default:
			_, _ = io.WriteString(writer, `{"token":"qa-token","expires_in":60}`)
		}
		return
	}
	peer.registryReads++
	expected := "/v2/supplier/db/blobs/" + peer.layer.Digest.String()
	if request.URL.Path != expected || request.URL.RawQuery != "" || request.Method != http.MethodGet || request.Header.Get("Cookie") != "" || request.Header.Get("Proxy-Authorization") != "" {
		peer.violations = true
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	if request.Header.Get("Authorization") != "Bearer qa-token" {
		if request.Header.Get("Authorization") != "" {
			peer.violations = true
		}
		realm, service, scope := "https://registry.test/token", "registry.test", "repository:supplier/db:pull"
		if peer.private {
			realm = "https://token.test/token"
		}
		switch peer.scenario {
		case "realm-unapproved", "private-realm-unapproved":
			realm = "https://outside.test/token"
		case "scope-push":
			scope = "repository:supplier/db:pull,push"
		case "scope-other", "private-scope-other":
			scope = "repository:other/db:pull"
		case "service-other", "private-service-other":
			service = "other.test"
		case "realm-http":
			realm = "http://registry.test/token"
		case "realm-query":
			realm += "?credential=not-retained"
		}
		challenge := fmt.Sprintf(`Bearer realm="%s",service="%s",scope="%s"`, realm, service, scope)
		if peer.scenario == "challenge-duplicate" {
			challenge += `,scope="repository:supplier/db:pull"`
		}
		writer.Header().Set("Www-Authenticate", challenge)
		if peer.scenario == "challenge-multiple" {
			writer.Header().Add("Www-Authenticate", challenge)
		}
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	start, end := int64(0), int64(0)
	if count, err := fmt.Sscanf(request.Header.Get("Range"), "bytes=%d-%d", &start, &end); count != 2 || err != nil || start < 0 || end < start || end >= peer.layer.Size || end-start >= databaseRangeBytes || request.Header.Get("Accept-Encoding") != "identity" {
		peer.violations = true
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	peer.ranges = append(peer.ranges, request.Header.Get("Range"))
	writer.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, peer.layer.Size))
	writer.Header().Set("Content-Length", fmt.Sprint(end-start+1))
	writer.WriteHeader(http.StatusPartialContent)
	_, _ = io.CopyBuffer(writer, io.LimitReader(databaseQABytes(7), end-start+1), make([]byte, 64<<10))
}

func (peer *databaseAuthenticationPeer) counts() (int, int, []string, bool) {
	peer.mutex.Lock()
	defer peer.mutex.Unlock()
	return peer.registryReads, peer.tokenReads, append([]string(nil), peer.ranges...), peer.violations
}

type databaseAuthenticationPeerTransport struct {
	base    http.RoundTripper
	peer    *url.URL
	allowed map[string]bool
	mutex   sync.Mutex
	blocked int
}

func (transport *databaseAuthenticationPeerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != "https" || !transport.allowed[request.URL.Host] {
		transport.mutex.Lock()
		transport.blocked++
		transport.mutex.Unlock()
		return nil, errors.New("owned fixture destination denied")
	}
	rewritten := request.Clone(request.Context())
	rewritten.URL.Scheme, rewritten.URL.Host = transport.peer.Scheme, transport.peer.Host
	rewritten.Host = request.URL.Host
	response, err := transport.base.RoundTrip(rewritten)
	if response != nil {
		response.Request = request
	}
	return response, err
}
func (transport *databaseAuthenticationPeerTransport) CloseIdleConnections() {
	if closer, ok := transport.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
func (transport *databaseAuthenticationPeerTransport) denied() int {
	transport.mutex.Lock()
	defer transport.mutex.Unlock()
	return transport.blocked
}

func databaseAuthenticationPeerClient(t *testing.T, peer *databaseAuthenticationPeer, private bool) (*http.Client, *databaseAuthenticationPeerTransport) {
	t.Helper()
	server := httptest.NewTLSServer(peer)
	t.Cleanup(server.Close)
	parsed, _ := url.Parse(server.URL)
	transport := &databaseAuthenticationPeerTransport{base: server.Client().Transport, peer: parsed, allowed: map[string]bool{"registry.test": true}}
	if private {
		transport.allowed["token.test"] = true
	}
	// Canonical production redirect checks stay underneath authentication. This
	// fixture never authorizes a redirect; token redirects must stop before dial.
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("owned fixture redirect denied") }}
	t.Cleanup(client.CloseIdleConnections)
	return client, transport
}

func databaseAuthenticationLayer(t *testing.T, size int64) ocispec.Descriptor {
	t.Helper()
	hash := sha256.New()
	if _, err := io.CopyBuffer(hash, io.LimitReader(databaseQABytes(7), size), make([]byte, 64<<10)); err != nil {
		t.Fatal("fixture digest unavailable")
	}
	return ocispec.Descriptor{MediaType: "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip", Digest: digest.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil))), Size: size}
}

func TestDatabaseRegistryAnonymousAndPrivateChallengeToImmutableRanges(t *testing.T) {
	layer := databaseAuthenticationLayer(t, databaseRangeBytes+17)
	for _, private := range []bool{false, true} {
		t.Run(fmt.Sprint("private=", private), func(t *testing.T) {
			peer := &databaseAuthenticationPeer{layer: layer, private: private}
			client, _ := databaseAuthenticationPeerClient(t, peer, private)
			policy := DatabaseRegistryAuthentication{}
			if private {
				policy = DatabaseRegistryAuthentication{RealmURL: "https://token.test/token", Service: "registry.test", Username: "qa-user", Password: "qa-password"}
			}
			authenticated, err := NewDatabaseRegistryClient(client, "registry.test/supplier/db", policy)
			if err != nil {
				t.Fatal("fixture constructor rejected")
			}
			// Constructor snapshots values; these changes must not alter requests.
			policy.RealmURL = "https://outside.test/token"
			policy.Password = "changed"
			destination := &databaseCountWriter{}
			if err := StreamSupplierDatabaseLayer(t.Context(), authenticated, "registry.test/supplier/db", layer, destination); err != nil {
				t.Fatal("challenge-aware exact transfer failed")
			}
			reads, tokens, ranges, violations := peer.counts()
			if reads != 3 || tokens != 1 || violations || destination.count != layer.Size || len(ranges) != 2 || ranges[0] != "bytes=0-67108863" || ranges[1] != "bytes=67108864-67108880" {
				t.Fatal("challenge/token/range identity or credential scope differs")
			}
		})
	}
}

func TestDatabaseRegistryChallengeCannotExpandAuthority(t *testing.T) {
	layer := databaseAuthenticationLayer(t, 17)
	for _, scenario := range []string{"realm-unapproved", "scope-push", "scope-other", "service-other", "realm-http", "realm-query", "challenge-duplicate", "challenge-multiple", "token-redirect", "token-duplicate", "token-oversized", "token-trailing", "token-conflicting", "private-realm-unapproved", "private-scope-other", "private-service-other", "configured-realm-transport-denied"} {
		t.Run(scenario, func(t *testing.T) {
			private := strings.HasPrefix(scenario, "token-") || strings.HasPrefix(scenario, "private-") || scenario == "configured-realm-transport-denied"
			peer := &databaseAuthenticationPeer{scenario: scenario, layer: layer, private: private}
			client, transport := databaseAuthenticationPeerClient(t, peer, private)
			policy := DatabaseRegistryAuthentication{}
			if private {
				policy = DatabaseRegistryAuthentication{RealmURL: "https://token.test/token", Service: "registry.test", Username: "qa-user", Password: "qa-password"}
			}
			if scenario == "configured-realm-transport-denied" {
				delete(transport.allowed, "token.test")
			}
			authenticated, err := NewDatabaseRegistryClient(client, "registry.test/supplier/db", policy)
			if err != nil {
				t.Fatal("fixture constructor rejected")
			}
			destination := &bytes.Buffer{}
			err = StreamSupplierDatabaseLayer(t.Context(), authenticated, "registry.test/supplier/db", layer, destination)
			if err == nil || destination.Len() != 0 || strings.Contains(err.Error(), "qa-password") || strings.Contains(err.Error(), "qa-token") || strings.Contains(err.Error(), "credential=") {
				t.Fatal("unsafe authentication accepted or sensitive diagnostic exposed")
			}
			reads, tokens, ranges, violations := peer.counts()
			if reads != 1 || len(ranges) != 0 || violations {
				t.Fatal("rejected challenge reached authenticated payload")
			}
			expectedTokens := 0
			if strings.HasPrefix(scenario, "token-") {
				expectedTokens = 1
			}
			if tokens != expectedTokens {
				t.Fatal("rejected challenge contacted unexpected token endpoint")
			}
			if scenario == "configured-realm-transport-denied" && transport.denied() != 1 {
				t.Fatal("token fetch bypassed operator transport")
			}
		})
	}
}

func TestDatabaseRegistryAuthenticationConstructorAndRequestScope(t *testing.T) {
	client := &http.Client{Transport: artifactTransport(func(*http.Request) (*http.Response, error) {
		t.Error("invalid input reached transport")
		return nil, errors.New("unexpected")
	})}
	for _, policy := range []DatabaseRegistryAuthentication{{Username: "user"}, {Username: "user", Password: "password"}, {RealmURL: "https://token.test/token"}, {RealmURL: "http://token.test/token", Service: "registry.test"}, {RealmURL: "https://token.test/token?scope=other", Service: "registry.test"}, {RealmURL: "https://user:password@token.test/token", Service: "registry.test"}} {
		if _, err := NewDatabaseRegistryClient(client, "registry.test/supplier/db", policy); err == nil {
			t.Fatal("invalid auth constructor accepted")
		}
	}
	authenticated, err := NewDatabaseRegistryClient(client, "registry.test/supplier/db", DatabaseRegistryAuthentication{})
	if err != nil {
		t.Fatal("fixture constructor rejected")
	}
	for _, location := range []string{"https://other.test/v2/supplier/db/blobs/sha256:x", "https://registry.test/v2/other/db/blobs/sha256:x", "https://registry.test/token?scope=other", "http://registry.test/v2/supplier/db/blobs/sha256:x"} {
		request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, location, nil)
		if _, err := authenticated.Do(request); err == nil {
			t.Fatal("caller URL expanded fixed repository")
		}
	}
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://registry.test/v2/supplier/db/blobs/sha256:x", nil)
	request.Header.Set("Authorization", "Bearer ambient")
	if _, err := authenticated.Do(request); err == nil {
		t.Fatal("ambient credential accepted")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	request, _ = http.NewRequestWithContext(canceled, http.MethodGet, "https://registry.test/v2/supplier/db/blobs/sha256:x", nil)
	// Avoid a test transport's intentional error: the adapter itself must check
	// cancellation before any HTTP request even when transport ignores contexts.
	if _, err := authenticated.Do(request); !errors.Is(err, context.Canceled) {
		t.Fatal("authentication lost cancellation identity")
	}
}
