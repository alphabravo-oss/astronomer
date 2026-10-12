package resolver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

type databasePublicationAuthenticationPeer struct {
	mutex                                   sync.Mutex
	scenario                                string
	private                                 bool
	tokenRequests, registryRequests, writes int
	violation                               bool
}

func (peer *databasePublicationAuthenticationPeer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	peer.mutex.Lock()
	defer peer.mutex.Unlock()
	if request.URL.Path == "/token" {
		peer.tokenRequests++
		user, password, basic := request.BasicAuth()
		if request.Method != http.MethodGet || request.URL.Query().Get("scope") != "repository:published/db:pull,push" || request.URL.Query().Get("service") != "registry.test" || peer.private && (!basic || user != "qa-user" || password != "qa-password") || !peer.private && request.Header.Get("Authorization") != "" {
			peer.violation = true
			writer.WriteHeader(403)
			return
		}
		_, _ = io.WriteString(writer, `{"token":"qa-token","expires_in":600}`)
		return
	}
	peer.registryRequests++
	if request.Header.Get("Authorization") != "Bearer qa-token" {
		if request.Header.Get("Authorization") != "" {
			peer.violation = true
		}
		realm, scope, service := "https://registry.test/token", "repository:published/db:pull,push", "registry.test"
		if peer.private {
			realm = "https://token.test/token"
		}
		switch peer.scenario {
		case "other-scope":
			scope = "repository:other/db:pull,push"
		case "delete-scope":
			scope += ",delete"
		case "other-realm":
			realm = "https://outside.test/token"
		case "other-service":
			service = "outside.test"
		case "unbalanced":
			scope += "\""
		}
		writer.Header().Set("Www-Authenticate", fmt.Sprintf(`Bearer realm="%s",service="%s",scope="%s"`, realm, service, scope))
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	if request.Header.Get("Cookie") != "" || request.Header.Get("Proxy-Authorization") != "" {
		peer.violation = true
		writer.WriteHeader(403)
		return
	}
	switch request.Method {
	case http.MethodPost:
		if request.URL.Path != "/v2/published/db/blobs/uploads/" || request.URL.RawQuery != "" {
			peer.violation = true
			writer.WriteHeader(400)
			return
		}
		writer.Header().Set("Location", "/v2/published/db/blobs/uploads/owned")
		writer.WriteHeader(202)
	case http.MethodPut:
		if request.URL.Path != "/v2/published/db/blobs/uploads/owned" || request.URL.Query().Get("digest") != "sha256:"+strings.Repeat("a", 64) {
			peer.violation = true
			writer.WriteHeader(400)
			return
		}
		content, err := io.ReadAll(io.LimitReader(request.Body, 65))
		if err != nil || !bytes.Equal(content, []byte("original layer")) {
			peer.violation = true
			writer.WriteHeader(400)
			return
		}
		peer.writes++
		writer.WriteHeader(201)
	default:
		peer.violation = true
		writer.WriteHeader(405)
	}
}
func (peer *databasePublicationAuthenticationPeer) counts() (int, int, int, bool) {
	peer.mutex.Lock()
	defer peer.mutex.Unlock()
	return peer.tokenRequests, peer.registryRequests, peer.writes, peer.violation
}
func databasePublicationAuthenticationClient(t *testing.T, peer *databasePublicationAuthenticationPeer) (*http.Client, *databaseAuthenticationPeerTransport) {
	t.Helper()
	server := httptest.NewTLSServer(peer)
	t.Cleanup(server.Close)
	parsed, _ := url.Parse(server.URL)
	transport := &databaseAuthenticationPeerTransport{base: server.Client().Transport, peer: parsed, allowed: map[string]bool{"registry.test": true, "token.test": peer.private}}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("owned fixture redirect denied") }}
	t.Cleanup(client.CloseIdleConnections)
	return client, transport
}
func TestDatabasePublicationAuthenticationKeepsExactProtectedUploadScope(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(fmt.Sprintf("private=%t", private), func(t *testing.T) {
			peer := &databasePublicationAuthenticationPeer{private: private}
			base, _ := databasePublicationAuthenticationClient(t, peer)
			policy := DatabaseRegistryAuthentication{}
			if private {
				policy = DatabaseRegistryAuthentication{RealmURL: "https://token.test/token", Service: "registry.test", Username: "qa-user", Password: "qa-password"}
			}
			client, err := NewDatabaseRegistryPublicationClient(base, "registry.test/published/db", policy)
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://registry.test/v2/published/db/blobs/uploads/", nil)
			response, err := client.Do(request)
			if err != nil || response.StatusCode != 202 {
				t.Fatal("actual scoped upload initialization failed", err)
			}
			_ = response.Body.Close()
			request, _ = http.NewRequestWithContext(t.Context(), http.MethodPut, "https://registry.test/v2/published/db/blobs/uploads/owned?digest=sha256:"+strings.Repeat("a", 64), bytes.NewReader([]byte("original layer")))
			response, err = client.Do(request)
			if err != nil || response.StatusCode != 201 {
				t.Fatal("actual cached scope upload failed", err)
			}
			_ = response.Body.Close()
			tokens, requests, writes, violation := peer.counts()
			if tokens != 1 || requests != 3 || writes != 1 || violation {
				t.Fatalf("owned challenge/token/POST/PUT did not preserve exact scope: %d %d %d %t", tokens, requests, writes, violation)
			}
		})
	}
}
func TestDatabasePublicationAuthenticationRefusesUnexpectedChallengeAndCallerGrant(t *testing.T) {
	for _, scenario := range []string{"other-scope", "delete-scope", "other-realm", "other-service", "unbalanced", "caller-authorization", "caller-cookie", "caller-proxy", "ordinary-pull"} {
		t.Run(scenario, func(t *testing.T) {
			peer := &databasePublicationAuthenticationPeer{scenario: scenario, private: true}
			base, transport := databasePublicationAuthenticationClient(t, peer)
			policy := DatabaseRegistryAuthentication{RealmURL: "https://token.test/token", Service: "registry.test", Username: "qa-user", Password: "qa-password"}
			constructor := NewDatabaseRegistryPublicationClient
			if scenario == "ordinary-pull" {
				constructor = NewDatabaseRegistryClient
			}
			client, err := constructor(base, "registry.test/published/db", policy)
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://registry.test/v2/published/db/blobs/uploads/", nil)
			switch scenario {
			case "caller-authorization":
				request.Header.Set("Authorization", "Bearer caller")
			case "caller-cookie":
				request.Header.Set("Cookie", "private=value")
			case "caller-proxy":
				request.Header.Set("Proxy-Authorization", "private")
			}
			response, err := client.Do(request)
			if response != nil {
				_ = response.Body.Close()
			}
			if err == nil {
				t.Fatal("unexpected challenge/caller authority allowed")
			}
			tokens, _, writes, violation := peer.counts()
			if tokens != 0 || writes != 0 || violation || transport.denied() != 0 {
				t.Fatal("refused authority reached token/write/outside fixture")
			}
		})
	}
}
func TestDatabasePublicationClosedRequestPathsCannotSelectNetworkOrAdditionalScope(t *testing.T) {
	for _, value := range []string{
		"DELETE https://registry.test/v2/published/db/manifests/current",
		"PATCH https://registry.test/v2/published/db/blobs/uploads/owned",
		"PUT https://outside.test/v2/published/db/blobs/uploads/owned?digest=sha256:" + strings.Repeat("a", 64),
		"PUT https://registry.test/v2/other/db/manifests/current",
		"PUT https://registry.test/v2/published/db/manifests/latest",
		"POST https://registry.test/v2/published/db/blobs/uploads/?mount=sha256:" + strings.Repeat("a", 64) + "&from=other/db",
		"PUT https://registry.test/v2/published/db/blobs/uploads/owned?digest=sha256:invalid",
		"PUT https://registry.test/v2/published/db/blobs/uploads/owned?digest=sha256:" + strings.Repeat("a", 64) + "&digest=sha256:" + strings.Repeat("a", 64),
		"PUT https://registry.test/v2/published/db/blobs/uploads/owned?digest=sha256:" + strings.Repeat("a", 64) + "&scope=repository:other/db:push",
	} {
		t.Run(value[:strings.IndexByte(value, ' ')]+fmt.Sprint(len(value)), func(t *testing.T) {
			method, raw, _ := strings.Cut(value, " ")
			request, _ := http.NewRequestWithContext(context.Background(), method, raw, nil)
			if validDatabaseRegistryRequest(request, "registry.test", "published/db", true) {
				t.Fatal("closed publisher request permitted unowned authority")
			}
		})
	}
	for _, reference := range []string{"current", "sha256:" + strings.Repeat("a", 64), "sha256-" + strings.Repeat("a", 64) + ".sig"} {
		request, _ := http.NewRequest(http.MethodPut, "https://registry.test/v2/published/db/manifests/"+reference, nil)
		if !validDatabaseRegistryRequest(request, "registry.test", "published/db", true) {
			t.Fatal("exact protected reference rejected")
		}
	}
}
