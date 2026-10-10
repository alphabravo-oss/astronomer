//go:build unix

package resolver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

func mountedReadRegistryFixture(t *testing.T) (DatabaseRegistryTrustOptions, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	requests, tokens := &atomic.Int64{}, &atomic.Int64{}
	var origin string
	server, roots, address := privateRegistryTLS(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Path == "/token" {
			tokens.Add(1)
			username, password, ok := request.BasicAuth()
			scope := request.URL.Query().Get("scope")
			pull := username == "owned-read-user" && password == "owned-read-password" && (scope == "repository:private/feed:pull" || scope == "repository:private/child:pull")
			push := username == "owned-push-user" && password == "owned-push-password" && scope == "repository:private/feed:pull,push"
			if !ok || request.URL.Query().Get("service") != "owned-read-service" || !pull && !push {
				writer.WriteHeader(http.StatusForbidden)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			if push {
				_, _ = writer.Write([]byte(`{"token":"owned-push-token"}`))
			} else {
				_, _ = writer.Write([]byte(`{"token":"owned-read-token"}`))
			}
			return
		}
		if request.Method == http.MethodPut {
			if request.Header.Get("Authorization") != "Bearer owned-push-token" {
				writer.Header().Set("Www-Authenticate", `Bearer realm="`+origin+`/token",service="owned-read-service",scope="repository:private/feed:pull,push"`)
				writer.WriteHeader(http.StatusUnauthorized)
				return
			}
			writer.WriteHeader(http.StatusCreated)
			return
		}
		repository := "private/feed"
		if strings.HasPrefix(request.URL.Path, "/v2/private/child/") {
			repository = "private/child"
		}
		if request.Header.Get("Authorization") != "Bearer owned-read-token" {
			writer.Header().Set("Www-Authenticate", `Bearer realm="`+origin+`/token",service="owned-read-service",scope="repository:`+repository+`:pull"`)
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = writer.Write([]byte("original owned private bytes"))
	}))
	origin = server.URL
	endpoint, err := url.Parse(origin)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "registry.pem"), roots, 0600); err != nil {
		t.Fatal(err)
	}
	writeMountedReadFixture(t, directory, "owned-read-password")
	return DatabaseRegistryTrustOptions{TrustDirectory: directory, RootsRef: "registry", Network: httpclient.DestinationPolicy{Restricted: true, Origins: []httpclient.TrustedOrigin{{URL: origin, CIDRs: []netip.Prefix{netip.PrefixFrom(address, 32)}}}}, Read: &DatabaseRegistryReadOptions{CredentialRef: "pull", Registry: endpoint.Host, RealmURL: origin + "/token", Service: "owned-read-service", Repositories: []string{endpoint.Host + "/private/feed", endpoint.Host + "/private/child"}}}, requests, tokens
}

func writeMountedReadFixture(t *testing.T, directory, password string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, "pull.json"), []byte(`{"schema":1,"username":"owned-read-user","password":"`+password+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseRegistryMountedPullUsesActualTLSAndExactChallengeScope(t *testing.T) {
	options, requests, tokens := mountedReadRegistryFixture(t)
	owner, err := CaptureDatabaseRegistryTransport(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	client := owner.Client()
	defer client.CloseIdleConnections()
	origin := options.Network.Origins[0].URL
	// Parent/feed, immutable child/config/payload, and legacy signature locators
	// all use the same mounted pull route, not publisher credentials.
	for _, path := range []string{"/v2/private/feed/manifests/current", "/v2/private/feed/manifests/sha256-" + strings.Repeat("a", 64) + ".sig", "/v2/private/child/manifests/sha256:" + strings.Repeat("b", 64), "/v2/private/child/blobs/sha256:" + strings.Repeat("c", 64)} {
		response, err := client.Get(origin + path)
		if err != nil {
			t.Fatal("actual private pull unavailable")
		}
		data, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || string(data) != "original owned private bytes" || response.StatusCode != http.StatusOK {
			t.Fatal("owned private bytes not reached")
		}
	}
	if tokens.Load() != 2 || requests.Load() < 8 {
		t.Fatal("independent exact repository token caches not used")
	}
	before := requests.Load()
	for _, mutation := range []string{"unknown-repository", "nested-repository", "push", "caller-auth", "caller-cookie", "caller-proxy-auth"} {
		t.Run(mutation, func(t *testing.T) {
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, origin+"/v2/private/feed/manifests/current", nil)
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "unknown-repository":
				request.URL.Path = "/v2/private/other/manifests/current"
			case "nested-repository":
				request.URL.Path = "/v2/private/feed/nested/manifests/current"
			case "push":
				request.Method = http.MethodPut
			case "caller-auth":
				request.Header.Set("Authorization", "Bearer caller-token")
			case "caller-cookie":
				request.Header.Set("Cookie", "caller=value")
			case "caller-proxy-auth":
				request.Header.Set("Proxy-Authorization", "Basic caller")
			}
			if _, err := client.Do(request); err == nil || requests.Load() != before {
				t.Fatal("caller expanded mounted pull authority")
			}
		})
	}
}

func TestDatabaseRegistryMountedPullRotationRebindsWithoutChangingRetainedScope(t *testing.T) {
	options, requests, _ := mountedReadRegistryFixture(t)
	owner, err := CaptureDatabaseRegistryTransport(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	roots, policy := owner.Fingerprints()
	origin := owner.RootOriginFingerprint()
	inspector, err := NewMountedDatabaseRegistryInspector(options)
	if err != nil {
		t.Fatal(err)
	}
	inspectorPolicy, inspectorOrigin := inspector.Bindings()
	if inspectorPolicy != policy || inspectorOrigin != origin {
		t.Fatal("restart policy differs from actual constructor policy")
	}
	// Caller mutation cannot grant a repository or redirect the frozen credential.
	options.Read.Repositories[0] = options.Read.Registry + "/private/other"
	writeMountedReadFixture(t, options.TrustDirectory, "rotated-read-password")
	if _, err := owner.Client().Get(options.Network.Origins[0].URL + "/v2/private/feed/manifests/current"); err == nil || requests.Load() != 0 || errors.Is(err, ErrMountedDatabaseTrustLost) {
		t.Fatal("rotated read credential either remained usable or became signing trust loss")
	}
	// Scope remains the original constructor scope; restore DATA before recapture.
	options.Read.Repositories[0] = options.Read.Registry + "/private/feed"
	next, err := CaptureDatabaseRegistryTransport(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	nextRoots, nextPolicy := next.Fingerprints()
	if nextRoots != roots || nextPolicy != policy || next.RootOriginFingerprint() != origin {
		t.Fatal("credential bytes leaked into retained scope identity")
	}
	if err := next.Revalidate(t.Context()); err != nil {
		t.Fatal("new captured credential did not rebind")
	}
	if err := os.Remove(filepath.Join(options.TrustDirectory, "pull.json")); err != nil {
		t.Fatal(err)
	}
	if err := next.Revalidate(t.Context()); err == nil || errors.Is(err, ErrMountedDatabaseTrustLost) {
		t.Fatal("missing read credential did not fail with a non-granting read error")
	}
	if _, err := inspector.Fingerprint(t.Context()); err != nil {
		t.Fatal("reduce-only public-root inspection incorrectly depends on a read credential")
	}
}

func TestDatabaseRegistryMountedPullClosedCredentialAndConstructorNegatives(t *testing.T) {
	for _, mutation := range []string{"partial", "unapproved-realm", "duplicate-repo", "other-registry", "tag", "traversal-ref", "missing", "symlink", "public-mode", "group-writable", "unknown-json", "duplicate-json", "too-large", "empty", "canceled"} {
		t.Run(mutation, func(t *testing.T) {
			options, requests, _ := mountedReadRegistryFixture(t)
			path := filepath.Join(options.TrustDirectory, "pull.json")
			ctx := t.Context()
			switch mutation {
			case "partial":
				options.Read.Service = ""
			case "unapproved-realm":
				options.Read.RealmURL = "https://unapproved.example.test/token"
			case "duplicate-repo":
				options.Read.Repositories[1] = options.Read.Repositories[0]
			case "other-registry":
				options.Read.Repositories[1] = "other.example.test/private/feed"
			case "tag":
				options.Read.Repositories[1] += ":mutable"
			case "traversal-ref":
				options.Read.CredentialRef = "../pull"
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(options.TrustDirectory, "registry.pem"), path); err != nil {
					t.Fatal(err)
				}
			case "public-mode":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "group-writable":
				if err := os.Chmod(path, 0660); err != nil {
					t.Fatal(err)
				}
			case "unknown-json":
				if err := os.WriteFile(path, []byte(`{"schema":1,"username":"owned-read-user","password":"owned-read-password","grant":true}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "duplicate-json":
				if err := os.WriteFile(path, []byte(`{"schema":1,"schema":1,"username":"owned-read-user","password":"owned-read-password"}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "too-large":
				if err := os.WriteFile(path, []byte(strings.Repeat("x", maxDatabaseReadCredentialBytes+1)), 0600); err != nil {
					t.Fatal(err)
				}
			case "empty":
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			owner, err := CaptureDatabaseRegistryTransport(ctx, options)
			if err == nil || owner != nil || requests.Load() != 0 || errors.Is(err, ErrMountedDatabaseTrustLost) {
				t.Fatal("invalid mounted read authority accepted or converted to public trust loss")
			}
			if mutation == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation identity lost")
			}
		})
	}
	credential := databaseRegistryReadCredential{username: "owned-private-user", password: "owned-private-password"}
	for _, value := range []any{credential, &credential} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%d"} {
			if text := fmt.Sprintf(format, value); text != "<database registry read credential>" {
				t.Fatal("credential formatting disclosed private material")
			}
		}
	}
}

func TestDatabaseRegistryMountedPullNeverSuppliesProtectedPushCredentials(t *testing.T) {
	options, _, tokens := mountedReadRegistryFixture(t)
	owner, err := CaptureDatabaseRegistryTransport(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	base, err := owner.PublicationAuthenticationClient(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer base.CloseIdleConnections()
	// An explicit protected client with no push credential cannot inherit the
	// mounted read credential, even though both use the same TLS/destination owner.
	anonymous, err := NewDatabaseRegistryPublicationClient(base, options.Read.Registry+"/private/feed", DatabaseRegistryAuthentication{RealmURL: options.Read.RealmURL, Service: options.Read.Service})
	if err != nil {
		t.Fatal(err)
	}
	response, err := anonymous.Get(options.Network.Origins[0].URL + "/v2/private/feed/manifests/current")
	if err == nil {
		_ = response.Body.Close()
		if response.StatusCode == http.StatusOK {
			t.Fatal("protected client inherited mounted pull credential")
		}
	}
	if tokens.Load() != 1 {
		t.Fatal("expected real failed anonymous token request")
	}
	push, err := NewDatabaseRegistryPublicationClient(base, options.Read.Registry+"/private/feed", DatabaseRegistryAuthentication{RealmURL: options.Read.RealmURL, Service: options.Read.Service, Username: "owned-push-user", Password: "owned-push-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer push.CloseIdleConnections()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPut, options.Network.Origins[0].URL+"/v2/private/feed/manifests/current", strings.NewReader("owned manifest"))
	if err != nil {
		t.Fatal(err)
	}
	response, err = push.Do(request)
	if err != nil {
		t.Fatal("independent protected push authentication failed")
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusCreated || tokens.Load() != 2 {
		t.Fatal("independent push scope did not reach actual owned TLS peer")
	}
}
