package resolver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type artifactTransport func(*http.Request) (*http.Response, error)

func (f artifactTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestArtifactManifestRejectsAmbiguityWithValidSubjectHash(t *testing.T) {
	payload := []byte("QA immutable payload")
	manifest := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: ocispec.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: digest.FromString("{}"), Size: 2}, Layers: []ocispec.Descriptor{{MediaType: "application/vnd.qa.payload", Digest: digest.FromBytes(payload), Size: int64(len(payload))}}}
	valid, _ := json.Marshal(manifest)
	for _, test := range []struct {
		name  string
		data  []byte
		valid bool
	}{
		{"valid", valid, true},
		{"duplicate", bytes.Replace(valid, []byte(`"schemaVersion":2`), []byte(`"schemaVersion":2,"schemaVersion":2`), 1), false},
		{"trailing document", append(append([]byte(nil), valid...), []byte(` {"schemaVersion":2}`)...), false},
		{"trailing malformed", append(append([]byte(nil), valid...), []byte(` invalid`)...), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := digest.FromBytes(test.data).String()
			payloadReads := 0
			client := &http.Client{Transport: artifactTransport(func(r *http.Request) (*http.Response, error) {
				data := test.data
				media := ocispec.MediaTypeImageManifest
				if strings.Contains(r.URL.Path, "/blobs/") {
					payloadReads++
					data = payload
					media = "application/vnd.qa.payload"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {media}, "Docker-Content-Digest": {digest.FromBytes(data).String()}, "Content-Length": {strconv.Itoa(len(data))}}, ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
			})}
			actual, err := fetchArtifactPayload(context.Background(), client, "qa.example.test/distribution", subject, "application/vnd.qa.payload", 1024)
			if test.valid {
				if err != nil || !bytes.Equal(actual, payload) || payloadReads != 1 {
					t.Fatalf("valid manifest rejected: %v", err)
				}
			} else if err == nil || payloadReads != 0 {
				t.Fatalf("ambiguous signed-subject JSON reached payload: err=%v reads=%d", err, payloadReads)
			}
		})
	}
}
