//go:build unix

package distribution

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
)

// Only the upload is rerouted to an owned real TLS peer. The genuine signed
// authority fixture still uses its canonical registry for every trust read.
type databasePublicationTLSUploadFixture struct {
	base   http.RoundTripper
	peer   http.RoundTripper
	origin *url.URL
	mutex  sync.Mutex
	body   *databasePublicationRequestBody
}

func (fixture *databasePublicationTLSUploadFixture) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host != "1.1.1.1" || !strings.HasPrefix(request.URL.Path, "/v2/endorsed/main/blobs/uploads/") {
		return fixture.base.RoundTrip(request)
	}
	clone := request.Clone(request.Context())
	location := *request.URL
	location.Scheme, location.Host = fixture.origin.Scheme, fixture.origin.Host
	clone.URL = &location
	clone.Host = fixture.origin.Host
	if request.Method == http.MethodPut {
		fixture.mutex.Lock()
		fixture.body, _ = request.Body.(*databasePublicationRequestBody)
		fixture.mutex.Unlock()
	}
	return fixture.peer.RoundTrip(clone)
}
func (fixture *databasePublicationTLSUploadFixture) capturedBody() *databasePublicationRequestBody {
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	return fixture.body
}
func (fixture *databasePublicationTLSUploadFixture) CloseIdleConnections() {
	for _, transport := range []http.RoundTripper{fixture.base, fixture.peer} {
		if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
			closer.CloseIdleConnections()
		}
	}
}

// Blocking one underlying Read lets a real HTTP transport overlap its response
// and request writer. Closing the source while active is recorded as a failure.
type databasePublicationBarrierReader struct {
	ctx                context.Context
	content            *bytes.Reader
	entered            chan struct{}
	release            chan struct{}
	once               sync.Once
	mutex              sync.Mutex
	reads              int
	active, closed     bool
	accessesAfterClose int
	shortAfterRelease  bool
}

func (reader *databasePublicationBarrierReader) Read(output []byte) (int, error) {
	reader.mutex.Lock()
	if reader.closed {
		reader.accessesAfterClose++
		reader.mutex.Unlock()
		return 0, io.ErrClosedPipe
	}
	reader.reads++
	block := reader.entered != nil && reader.reads == 2
	reader.active = true
	reader.mutex.Unlock()
	if block {
		reader.once.Do(func() { close(reader.entered) })
		select {
		case <-reader.release:
		case <-reader.ctx.Done():
		}
	}
	reader.mutex.Lock()
	defer reader.mutex.Unlock()
	reader.active = false
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	// This fixture deliberately stops the in-flight second read after the
	// real peer's early response. Releasing ordinary remaining bytes could
	// finish the entire body before transport cancellation and would not
	// establish a partial upload prerequisite.
	if block && reader.shortAfterRelease {
		return 0, io.ErrUnexpectedEOF
	}
	return reader.content.Read(output)
}
func (reader *databasePublicationBarrierReader) Close() error {
	reader.mutex.Lock()
	defer reader.mutex.Unlock()
	if reader.active {
		return errors.New("fixture source closed before reader joined")
	}
	reader.closed = true
	return nil
}
func (reader *databasePublicationBarrierReader) counts() (int, int) {
	reader.mutex.Lock()
	defer reader.mutex.Unlock()
	return reader.reads, reader.accessesAfterClose
}

type databasePublicationUploadResult struct {
	joined bool
	err    error
}

func publicationTLSRegistry(t *testing.T, peer *httptest.Server) (*DatabasePublicationRegistry, *databasePublicationTLSUploadFixture) {
	t.Helper()
	origin, err := url.Parse(peer.URL)
	if err != nil {
		t.Fatal(err)
	}
	signer, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	var upload *databasePublicationTLSUploadFixture
	authority, proof, _ := publicationBaseFixtureWithSigner(t, signer, public, func(client *http.Client) *http.Client {
		clone := *client
		upload = &databasePublicationTLSUploadFixture{base: client.Transport, peer: peer.Client().Transport, origin: origin}
		clone.Transport = upload
		return &clone
	})
	registry, err := authority.PublicationRegistry(t.Context(), proof, "1.1.1.1/endorsed/main", resolver.DatabaseRegistryAuthentication{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Close)
	return registry, upload
}

func TestDatabasePublicationRealTLSEarlyResponseJoinsBeforeSourceClose(t *testing.T) {
	for _, cancelParent := range []bool{false, true} {
		name := "release-read"
		if cancelParent {
			name = "cancel-read"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseRead := func() { releaseOnce.Do(func() { close(release) }) }
			peerRelease := make(chan struct{})
			var peerOnce sync.Once
			releasePeer := func() { peerOnce.Do(func() { close(peerRelease) }) }
			responseSent := make(chan struct{})
			peer := httptest.NewTLSServer(http.HandlerFunc(func(output http.ResponseWriter, request *http.Request) {
				if request.Method == http.MethodPost {
					output.Header().Set("Location", "/v2/endorsed/main/blobs/uploads/owned")
					output.WriteHeader(http.StatusAccepted)
					return
				}
				if request.Method != http.MethodPut {
					output.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				if err := http.NewResponseController(output).EnableFullDuplex(); err != nil {
					output.WriteHeader(http.StatusInternalServerError)
					return
				}
				var initial [1]byte
				if _, err := io.ReadFull(request.Body, initial[:]); err != nil {
					return
				}
				output.Header().Set("Content-Length", "0")
				output.WriteHeader(http.StatusCreated)
				if err := http.NewResponseController(output).Flush(); err != nil {
					return
				}
				close(responseSent)
				select {
				case <-peerRelease:
				case <-request.Context().Done():
				}
			}))
			t.Cleanup(func() { cancel(); releaseRead(); releasePeer(); peer.CloseClientConnections(); peer.Close() })
			registry, upload := publicationTLSRegistry(t, peer)
			content := bytes.Repeat([]byte("original supplier bytes"), 100000)
			reader := &databasePublicationBarrierReader{ctx: ctx, content: bytes.NewReader(content), entered: make(chan struct{}), release: release, shortAfterRelease: true}
			descriptor := ocispec.Descriptor{MediaType: "application/octet-stream", Digest: digest.FromBytes(content), Size: int64(len(content))}
			result := make(chan databasePublicationUploadResult, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				joined, err := registry.pushOwnedBlob(ctx, descriptor, reader)
				result <- databasePublicationUploadResult{joined, err}
			}()
			// Every failure path unblocks the controlled source and peer, then explicitly
			// joins the upload before closing the test server. An unjoined child is RED.
			defer func() {
				cancel()
				releaseRead()
				releasePeer()
				select {
				case <-done:
				case <-time.After(8 * time.Second):
					t.Error("upload cleanup remained unjoined")
				}
				peer.CloseClientConnections()
				peer.Close()
			}()
			wait := func(event <-chan struct{}, label string) {
				t.Helper()
				select {
				case <-event:
				case <-time.After(4 * time.Second):
					t.Fatalf("real TLS %s barrier absent", label)
				}
			}
			wait(reader.entered, "source-read")
			wait(responseSent, "early-response")
			select {
			case <-result:
				t.Fatal("upload returned while underlying source Read was active")
			default:
			}
			if cancelParent {
				cancel()
			} else {
				releaseRead()
			}
			var actual databasePublicationUploadResult
			select {
			case actual = <-result:
			case <-time.After(8 * time.Second):
				t.Fatal("transport body close was not joined")
			}
			if !actual.joined || actual.err == nil {
				t.Fatalf("partial early response qualified upload or failed to join: joined=%t errorPresent=%t", actual.joined, actual.err != nil)
			}
			if cancelParent && !errors.Is(actual.err, context.Canceled) {
				t.Fatal("parent cancellation lost its canonical error")
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			before, _ := reader.counts()
			body := upload.capturedBody()
			if body == nil {
				t.Fatal("real transport body adapter was not exercised")
			}
			copied, joinError := body.waitJoined()
			if joinError != nil || copied <= 0 || copied >= descriptor.Size {
				t.Fatal("early-response fixture did not establish a joined partial body")
			}
			if _, err := body.Read(make([]byte, 1)); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatal("sealed transport accessed source")
			}
			after, violations := reader.counts()
			if before != after || violations != 0 {
				t.Fatal("underlying source accessed after joined close")
			}
		})
	}
}

func TestDatabasePublicationRealTLSCompleteBodyQualifiesOnlyAfterJoin(t *testing.T) {
	content := bytes.Repeat([]byte("unchanged supplier payload"), 4000)
	peer := httptest.NewTLSServer(http.HandlerFunc(func(output http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			output.Header().Set("Location", "/v2/endorsed/main/blobs/uploads/owned")
			output.WriteHeader(http.StatusAccepted)
			return
		}
		if request.Method != http.MethodPut {
			output.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		copied, err := io.Copy(io.Discard, io.LimitReader(request.Body, int64(len(content))+1))
		if err != nil || copied != int64(len(content)) {
			output.WriteHeader(http.StatusBadRequest)
			return
		}
		output.WriteHeader(http.StatusCreated)
	}))
	defer peer.Close()
	registry, upload := publicationTLSRegistry(t, peer)
	reader := &databasePublicationBarrierReader{ctx: t.Context(), content: bytes.NewReader(content)}
	descriptor := ocispec.Descriptor{MediaType: "application/octet-stream", Digest: digest.FromBytes(content), Size: int64(len(content))}
	joined, err := registry.pushOwnedBlob(t.Context(), descriptor, reader)
	if !joined || err != nil {
		t.Fatalf("complete owned TLS upload failed: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	body := upload.capturedBody()
	if body == nil {
		t.Fatal("transport body adapter absent")
	}
	if _, err := body.Read(make([]byte, 1)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("closed transport read was not sealed")
	}
	_, violations := reader.counts()
	if violations != 0 {
		t.Fatal("post-close source access")
	}
}

func TestDatabasePublicationBodyMissingCloseAcknowledgementRetainsSource(t *testing.T) {
	reader := &databasePublicationBarrierReader{ctx: t.Context(), content: bytes.NewReader([]byte("owned payload"))}
	body := newDatabasePublicationRequestBody(t.Context(), reader)
	if _, err := body.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := body.waitJoined(); err == nil || !strings.Contains(err.Error(), "unjoined") {
		t.Fatal("missing transport Close acknowledged as joined")
	}
	reader.mutex.Lock()
	closed := reader.closed
	reader.mutex.Unlock()
	if closed {
		t.Fatal("unjoined failure closed caller-owned source")
	}
	// This direct unit fixture owns no transport or background reader. Only now
	// does its owner explicitly close and join the adapter for finite cleanup.
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if copied, err := body.waitJoined(); err != nil || copied != 1 {
		t.Fatal("explicit owner close did not seal exact count")
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
}
