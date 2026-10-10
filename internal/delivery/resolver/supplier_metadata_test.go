package resolver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestSupplierMetadataPinsOneResolutionWithoutSignatureOrPayloadReads(t *testing.T) {
	for _, name := range []string{"valid", "duplicate-manifest", "changed-manifest", "oversized-layer", "external-layer", "inline-config", "changed-config", "duplicate-config", "wrong-resolution-media"} {
		t.Run(name, func(t *testing.T) {
			config := []byte(`{}`)
			if name == "duplicate-config" {
				config = []byte(`{"version":1,"version":1}`)
			}
			layer := ocispec.Descriptor{MediaType: "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip", Digest: digest.FromString("QA database not downloaded"), Size: 64 << 20}
			configuration := ocispec.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: digest.FromBytes(config), Size: int64(len(config))}
			switch name {
			case "oversized-layer":
				layer.Size = 2<<30 + 1
			case "external-layer":
				layer.URLs = []string{"https://unapproved.example.test/database"}
			case "inline-config":
				configuration.Data = config
			}
			manifest := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: configuration, Layers: []ocispec.Descriptor{layer}}
			original, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if name == "duplicate-manifest" {
				original = bytes.Replace(original, []byte(`"schemaVersion":2`), []byte(`"schemaVersion":2,"schemaVersion":2`), 1)
			}
			subject := digest.FromBytes(original).String()
			var mutex sync.Mutex
			reads := map[string]int{}
			client := &http.Client{Transport: artifactTransport(func(request *http.Request) (*http.Response, error) {
				mutex.Lock()
				reads[request.Method+" "+request.URL.Path]++
				count := reads[request.Method+" "+request.URL.Path]
				mutex.Unlock()
				media, body := ocispec.MediaTypeImageManifest, original
				switch {
				case request.Method == http.MethodHead && strings.HasSuffix(request.URL.Path, "/manifests/2"):
					if count > 1 {
						body = []byte(`{"mutable":"new snapshot"}`)
					}
					if name == "wrong-resolution-media" {
						media = ocispec.MediaTypeImageIndex
					}
				case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/manifests/"+subject):
					if name == "changed-manifest" {
						body = []byte(`{}`)
					}
				case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/blobs/"+configuration.Digest.String()):
					body, media = config, "application/octet-stream"
					if name == "changed-config" {
						body = []byte(`[]`)
					}
				default:
					return nil, errors.New("fixture forbids supplier signature, layer or unrelated request")
				}
				header := http.Header{"Content-Type": {media}, "Docker-Content-Digest": {digest.FromBytes(body).String()}, "Content-Length": {strconv.Itoa(len(body))}}
				length := int64(len(body))
				if request.Method == http.MethodHead {
					body = nil
				}
				return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: length, Request: request}, nil
			})}
			actualSubject, metadata, err := CaptureSupplierDatabaseMetadata(t.Context(), client, "reviewed.example.test/main", "2", layer.MediaType)
			if name == "valid" {
				if err != nil || actualSubject != subject || !bytes.Equal(metadata.Manifest, original) || !bytes.Equal(metadata.Config, config) || metadata.Layer.Digest != layer.Digest {
					t.Fatalf("supplier capture failed: %v", err)
				}
			} else if err == nil {
				t.Fatal("invalid new supplier metadata accepted")
			}
			mutex.Lock()
			defer mutex.Unlock()
			if reads["HEAD /v2/main/manifests/2"] != 1 {
				t.Fatal("discovery tag was not resolved exactly once")
			}
			for path := range reads {
				if path != "HEAD /v2/main/manifests/2" && path != "GET /v2/main/manifests/"+subject && path != "GET /v2/main/blobs/"+configuration.Digest.String() {
					t.Fatal("capture fetched layer, signature or unrelated identity")
				}
			}
		})
	}
}

func TestSupplierMetadataRefusesUnownedSchemaOrCanceledOperationBeforeFetch(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: artifactTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("fixture refuses request")
	})}
	media := "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip"
	for _, tag := range []string{"latest", "1", "2@sha256:invalid"} {
		if _, _, err := CaptureSupplierDatabaseMetadata(t.Context(), client, "reviewed.example.test/main", tag, media); err == nil {
			t.Fatal("unowned discovery schema accepted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := CaptureSupplierDatabaseMetadata(ctx, client, "reviewed.example.test/main", "2", media); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation identity lost")
	}
	if calls != 0 {
		t.Fatal("invalid capture reached transport")
	}
}
