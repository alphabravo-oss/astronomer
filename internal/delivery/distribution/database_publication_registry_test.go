//go:build unix

package distribution

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
)

type databasePublicationLocationFixture struct {
	base        http.RoundTripper
	location    string
	mutex       sync.Mutex
	posts, puts int
}

func (fixture *databasePublicationLocationFixture) RoundTrip(request *http.Request) (*http.Response, error) {
	fixture.mutex.Lock()
	if request.Method == http.MethodPost {
		fixture.posts++
	}
	if request.Method == http.MethodPut {
		fixture.puts++
	}
	fixture.mutex.Unlock()
	response, err := fixture.base.RoundTrip(request)
	if err == nil && request.Method == http.MethodPost {
		response.Header.Set("Location", fixture.location)
	}
	return response, err
}
func (fixture *databasePublicationLocationFixture) counts() (int, int) {
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	return fixture.posts, fixture.puts
}
func (fixture *databasePublicationLocationFixture) CloseIdleConnections() {
	if closer, ok := fixture.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func TestDatabasePublicationRejectsUploadLocationBeforeBodyWrite(t *testing.T) {
	for _, test := range []struct{ name, location string }{
		{"different-host", "https://outside.test/v2/endorsed/main/blobs/uploads/1"},
		{"different-repository", "/v2/other/main/blobs/uploads/1"},
		{"credentials", "https://fixture-user:fixture-password@1.1.1.1/v2/endorsed/main/blobs/uploads/1"},
		{"plain-http", "http://1.1.1.1/v2/endorsed/main/blobs/uploads/1"},
		{"mount-scope", "/v2/endorsed/main/blobs/uploads/1?mount=sha256:" + strings.Repeat("a", 64)},
		{"inherited-digest", "/v2/endorsed/main/blobs/uploads/1?digest=sha256:" + strings.Repeat("a", 64)},
		{"duplicate-state", "/v2/endorsed/main/blobs/uploads/1?_state=a&_state=b"},
		{"nested-upload-id", "/v2/endorsed/main/blobs/uploads/nested/1"},
		{"escaped-path", "/v2/endorsed/main/blobs/uploads/%2f1"},
		{"empty-query", "/v2/endorsed/main/blobs/uploads/1?"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var fixture *databasePublicationLocationFixture
			signer, key, err := signature.NewDefaultECDSASignerVerifier()
			if err != nil {
				t.Fatal(err)
			}
			public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
			if err != nil {
				t.Fatal(err)
			}
			authority, proof, _ := publicationBaseFixtureWithSigner(t, signer, public, func(client *http.Client) *http.Client {
				clone := *client
				fixture = &databasePublicationLocationFixture{base: client.Transport, location: test.location}
				clone.Transport = fixture
				return &clone
			})
			registry, err := authority.PublicationRegistry(t.Context(), proof, "1.1.1.1/endorsed/main", resolver.DatabaseRegistryAuthentication{})
			if err != nil {
				t.Fatal(err)
			}
			defer registry.Close()
			content := []byte("original supplier layer")
			descriptor := ocispec.Descriptor{MediaType: "application/octet-stream", Digest: digest.FromBytes(content), Size: int64(len(content))}
			if err := registry.pushBlob(t.Context(), descriptor, bytes.NewReader(content)); err == nil {
				t.Fatal("unowned upload location accepted")
			}
			posts, puts := fixture.counts()
			if posts != 1 || puts != 0 {
				t.Fatal("invalid upload location caused a payload write")
			}
		})
	}
}

func TestDatabasePublicationResponseBodiesRemainFiniteAndClosed(t *testing.T) {
	for _, size := range []int{0, 64 << 10, (64 << 10) + 1} {
		body := &databasePublicationResponseBodyFixture{Reader: bytes.NewReader(make([]byte, size))}
		err := closeDatabasePublicationResponse(&http.Response{Body: body})
		if (err != nil) != (size > 64<<10) || !body.closed {
			t.Fatal("response bound or owned close was lost")
		}
	}
}

type databasePublicationResponseBodyFixture struct {
	io.Reader
	closed bool
}

func (body *databasePublicationResponseBodyFixture) Close() error { body.closed = true; return nil }
