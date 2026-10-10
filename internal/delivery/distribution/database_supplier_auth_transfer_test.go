//go:build unix

package distribution

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
)

type supplierAuthenticatedPeer struct {
	fixture    *registryFixture
	repository string
	payload    []byte
	digest     string
	mutex      sync.Mutex
	tokens     int
	challenges int
	layerReads int
	violations bool
}

func (peer *supplierAuthenticatedPeer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	peer.mutex.Lock()
	defer peer.mutex.Unlock()
	if request.URL.Path == "/token" {
		peer.tokens++
		username, password, basic := request.BasicAuth()
		if request.Method != http.MethodGet || !basic || username != "qa-user" || password != "qa-password" || request.URL.Query().Get("service") != "1.1.1.1" || request.URL.Query().Get("scope") != "repository:"+peer.repository+":pull" || len(request.URL.Query()) != 2 {
			peer.violations = true
			writer.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(writer, `{"token":"qa-token"}`)
		return
	}
	if request.Header.Get("Authorization") != "Bearer qa-token" {
		if request.Header.Get("Authorization") != "" {
			peer.violations = true
		}
		peer.challenges++
		writer.Header().Set("Www-Authenticate", fmt.Sprintf(`Bearer realm="https://1.1.1.1/token",service="1.1.1.1",scope="repository:%s:pull"`, peer.repository))
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	if request.URL.Path == "/v2/"+peer.repository+"/blobs/"+peer.digest {
		peer.layerReads++
		if request.Header.Get("Range") != fmt.Sprintf("bytes=0-%d", len(peer.payload)-1) || request.Header.Get("Accept-Encoding") != "identity" || request.URL.RawQuery != "" {
			peer.violations = true
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(peer.payload)-1, len(peer.payload)))
		writer.Header().Set("Content-Length", fmt.Sprint(len(peer.payload)))
		writer.WriteHeader(http.StatusPartialContent)
		_, _ = writer.Write(peer.payload)
		return
	}
	peer.fixture.ServeHTTP(writer, request)
}

func (peer *supplierAuthenticatedPeer) counts() (int, int, int, bool) {
	peer.mutex.Lock()
	defer peer.mutex.Unlock()
	return peer.tokens, peer.challenges, peer.layerReads, peer.violations
}

type supplierAuthenticatedRoutingTransport struct{ base registryRoutingTransport }

func (transport supplierAuthenticatedRoutingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	// The routing fixture must retain the logical registry URL for ORAS's actual
	// same-origin challenge check; it cannot pretend a changed peer is a registry.
	if response != nil {
		response.Request = request
	}
	return response, err
}
func (transport supplierAuthenticatedRoutingTransport) CloseIdleConnections() {
	if closer, ok := transport.base.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func TestSupplierAuthenticatedCaptureAndTransferShareExactOwnedScope(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, role := range []string{"trivy-database", "trivy-java-database"} {
		t.Run(role, func(t *testing.T) {
			registry, options, payload, expected := supplierDatabaseFixture(t, role, now, nil)
			peer := &supplierAuthenticatedPeer{fixture: registry.fixture, repository: strings.TrimPrefix(options.Repository, "1.1.1.1/"), payload: payload, digest: contentDigest(payload)}
			server := httptest.NewServer(peer)
			t.Cleanup(server.Close)
			options.Client = &http.Client{Transport: supplierAuthenticatedRoutingTransport{base: registryRoutingTransport{fixture: registry.fixture, target: strings.TrimPrefix(server.URL, "http://"), base: server.Client().Transport}}}
			options.Authentication = resolver.DatabaseRegistryAuthentication{RealmURL: "https://1.1.1.1/token", Service: "1.1.1.1", Username: "qa-user", Password: "qa-password"}
			inspector, err := NewSupplierDatabaseInspector(options)
			if err != nil {
				t.Fatal("authenticated supplier constructor rejected")
			}
			t.Cleanup(inspector.options.Client.CloseIdleConnections)
			options.Authentication.Password = "changed"
			captured, err := inspector.Capture(t.Context())
			if err != nil {
				t.Fatal("real challenged metadata capture failed")
			}
			tokens, challenges, layers, violations := peer.counts()
			if tokens != 1 || challenges != 1 || layers != 0 || violations {
				t.Fatal("capture token authority or no-payload boundary differs")
			}
			output := filepath.Join(t.TempDir(), "supplier.tar.gz")
			verified, err := inspector.Transfer(t.Context(), captured, output, now)
			if err != nil {
				t.Fatal("challenge-aware staged transfer failed")
			}
			snapshot, ok := verified.Snapshot()
			tokens, challenges, layers, violations = peer.counts()
			data, err := os.ReadFile(output)
			if err != nil || !ok || snapshot.Metadata != expected || snapshot.Layer.Digest != contentDigest(data) || tokens != 1 || challenges != 1 || layers != 1 || violations {
				t.Fatal("capture and transfer lost unchanged payload or scoped token reuse")
			}
			assertNoSupplierTemporary(t, filepath.Dir(output))
		})
	}
}

func TestSupplierPrivateAuthenticationRequiresExplicitRealmBeforeCapture(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	_, options, _, _ := supplierDatabaseFixture(t, "trivy-database", now, nil)
	calls := 0
	options.Client = &http.Client{Transport: supplierAuthRefusedTransport{calls: &calls}}
	options.Authentication = resolver.DatabaseRegistryAuthentication{Username: "qa-user", Password: "qa-password"}
	if _, err := NewSupplierDatabaseInspector(options); err == nil || calls != 0 {
		t.Fatal("private credential without exact owned realm contacted supplier")
	}
}

type supplierAuthRefusedTransport struct{ calls *int }

func (transport supplierAuthRefusedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	(*transport.calls)++
	return nil, errors.New("fixture should not be requested")
}
