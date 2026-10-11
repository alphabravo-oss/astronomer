package resolver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func workloadMetadataFixture(t *testing.T) (ocispec.Manifest, []byte, []byte) {
	t.Helper()
	layer := []byte("owned immutable workload layer")
	configuration := ocispec.Image{Platform: ocispec.Platform{Architecture: "amd64", OS: "linux"}, RootFS: ocispec.RootFS{Type: "layers", DiffIDs: []digest.Digest{digest.FromBytes(layer)}}}
	config, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	manifest := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: ocispec.Descriptor{MediaType: ocispec.MediaTypeImageConfig, Digest: digest.FromBytes(config), Size: int64(len(config))}, Layers: []ocispec.Descriptor{{MediaType: ocispec.MediaTypeImageLayer, Digest: digest.FromBytes(layer), Size: int64(len(layer))}}}
	return manifest, config, layer
}

func TestWorkloadImageMetadataIsExactPlatformContentNotTagAuthority(t *testing.T) {
	for _, name := range []string{"fetched-config", "inline-config", "wrong-platform", "wrong-os", "rootfs-mismatch", "mutable-tag", "bad-config-hash", "bad-manifest-hash", "unknown-manifest-field", "duplicate-manifest-field", "external-config", "external-layer", "inline-layer", "unknown-layer-media", "zero-layer-size", "too-many-layers", "duplicate-layer-conflict", "foreign-subject", "wrong-header-media", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			manifest, config, _ := workloadMetadataFixture(t)
			switch name {
			case "inline-config":
				manifest.Config.Data = config
			case "wrong-platform", "wrong-os", "rootfs-mismatch":
				var image ocispec.Image
				if err := json.Unmarshal(config, &image); err != nil {
					t.Fatal(err)
				}
				switch name {
				case "wrong-platform":
					image.Architecture = "arm64"
				case "wrong-os":
					image.OS = "windows"
				default:
					image.RootFS.DiffIDs = nil
				}
				var err error
				config, err = json.Marshal(image)
				if err != nil {
					t.Fatal(err)
				}
				manifest.Config.Digest = digest.FromBytes(config)
				manifest.Config.Size = int64(len(config))
			case "external-config":
				manifest.Config.URLs = []string{"https://unapproved.example.test/config"}
			case "external-layer":
				manifest.Layers[0].URLs = []string{"https://unapproved.example.test/layer"}
			case "inline-layer":
				manifest.Layers[0].Data = []byte("foreign")
			case "unknown-layer-media":
				manifest.Layers[0].MediaType = "application/octet-stream"
			case "zero-layer-size":
				manifest.Layers[0].Size = 0
			case "too-many-layers":
				for len(manifest.Layers) < 33 {
					manifest.Layers = append(manifest.Layers, manifest.Layers[0])
				}
			case "duplicate-layer-conflict":
				other := manifest.Layers[0]
				other.MediaType = ocispec.MediaTypeImageLayerGzip
				manifest.Layers = append(manifest.Layers, other)
			case "foreign-subject":
				foreign := manifest.Config
				manifest.Subject = &foreign
			}
			payload, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if name == "unknown-manifest-field" {
				payload = append(payload[:len(payload)-1], []byte(`,"caller_grant":true}`)...)
			}
			if name == "duplicate-manifest-field" {
				payload = append(payload[:len(payload)-1], []byte(`,"config":{}}`)...)
			}
			subject := digest.FromBytes(payload).String()
			if name == "bad-manifest-hash" {
				subject = "sha256:" + strings.Repeat("a", 64)
			}
			requests := 0
			configRequests := 0
			client := &http.Client{Transport: artifactTransport(func(request *http.Request) (*http.Response, error) {
				requests++
				var body []byte
				media := manifest.MediaType
				switch request.URL.Path {
				case "/v2/workload/app/manifests/" + subject:
					body = payload
				case "/v2/workload/app/blobs/" + manifest.Config.Digest.String():
					body = config
					media = manifest.Config.MediaType
					configRequests++
					if name == "bad-config-hash" {
						body = bytes.Repeat([]byte{0}, len(config))
					}
				default:
					t.Errorf("unexpected workload request: method/path must be exact immutable manifest/config")
					return nil, fmt.Errorf("unexpected request")
				}
				if name == "wrong-header-media" {
					media = "application/octet-stream"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {media}, "Docker-Content-Digest": {digest.FromBytes(body).String()}}, ContentLength: int64(len(body)), Body: io.NopCloser(bytes.NewReader(body)), Request: request}, nil
			})}
			ctx := t.Context()
			repository := "registry.example.test/workload/app"
			if name == "mutable-tag" {
				repository += ":current"
			}
			if name == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			result, err := FetchWorkloadImageMetadata(ctx, client, repository, subject, "amd64")
			positive := name == "fetched-config" || name == "inline-config"
			if positive {
				if err != nil || !bytes.Equal(result.Manifest, payload) || !bytes.Equal(result.Config, config) || len(result.Layers) != 1 {
					t.Fatalf("immutable content capture failed: %v", err)
				}
				if name == "inline-config" && configRequests != 0 {
					t.Fatal("inline bytes replaced by network config")
				}
			} else if err == nil {
				t.Fatal("invalid workload content accepted")
			}
			if (name == "mutable-tag" || name == "cancelled") && requests != 0 {
				t.Fatal("invalid source/cancellation emitted network request")
			}
		})
	}
}

func TestWorkloadImageLayerUsesSameExactRangeAndCompleteDigest(t *testing.T) {
	for _, name := range []string{"valid", "changed", "200", "wrong-range", "truncated", "unsupported-media", "external", "invalid-digest", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			body := []byte("immutable workload layer bytes")
			descriptor := ocispec.Descriptor{Digest: digest.FromBytes(body), Size: int64(len(body)), MediaType: ocispec.MediaTypeImageLayerGzip}
			switch name {
			case "unsupported-media":
				descriptor.MediaType = "application/octet-stream"
			case "external":
				descriptor.URLs = []string{"https://unapproved.example.test"}
			case "invalid-digest":
				descriptor.Digest = "sha256:../../foreign"
			}
			requests := 0
			client := &http.Client{Transport: artifactTransport(func(request *http.Request) (*http.Response, error) {
				requests++
				if request.URL.Path != "/v2/workload/app/blobs/"+descriptor.Digest.String() || request.Header.Get("Range") != fmt.Sprintf("bytes=0-%d", len(body)-1) || request.Header.Get("Accept-Encoding") != "identity" {
					t.Fatal("workload stream not bound to original exact blob/range")
				}
				status := 206
				rangeValue := fmt.Sprintf("bytes 0-%d/%d", len(body)-1, len(body))
				payload := bytes.Clone(body)
				switch name {
				case "changed":
					payload[0] ^= 1
				case "200":
					status = 200
				case "wrong-range":
					rangeValue = "bytes 1-2/3"
				case "truncated":
					payload = payload[:len(payload)-1]
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Range": {rangeValue}}, ContentLength: int64(len(payload)), Body: io.NopCloser(bytes.NewReader(payload)), Request: request}, nil
			})}
			ctx := t.Context()
			if name == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			var output bytes.Buffer
			err := StreamWorkloadImageLayer(ctx, client, "registry.example.test/workload/app", descriptor, &output)
			if name == "valid" {
				if err != nil || !bytes.Equal(output.Bytes(), body) {
					t.Fatalf("complete exact workload transfer failed: %v", err)
				}
			} else if err == nil {
				t.Fatal("changed/unbounded workload layer accepted")
			}
			if (name == "unsupported-media" || name == "external" || name == "invalid-digest" || name == "cancelled") && requests != 0 {
				t.Fatal("invalid workload claim requested payload")
			}
		})
	}
}
