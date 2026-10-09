package distribution

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestOfflineStoreOwnsBytesAndCannotDelegateNetwork(t *testing.T) {
	config := []byte("{}")
	payload := []byte("closed payload")
	signaturePayload := []byte("closed signature payload")
	manifest := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: ocispec.Descriptor{Digest: digest.FromBytes(config), Size: int64(len(config))}, Layers: []ocispec.Descriptor{{Digest: digest.FromBytes(payload), Size: int64(len(payload))}}}
	signatureManifest := manifest
	signatureManifest.Layers = []ocispec.Descriptor{{MediaType: "application/vnd.dev.cosign.simplesigning.v1+json", Digest: digest.FromBytes(signaturePayload), Size: int64(len(signaturePayload))}}
	manifestBytes, _ := json.Marshal(manifest)
	signatureBytes, _ := json.Marshal(signatureManifest)
	root := contentDigest(manifestBytes)
	subjects := []StoredSubject{{Repository: "private.example.test/distribution", Digest: root, SignatureDigest: contentDigest(signatureBytes)}}
	blobs := map[string][]byte{contentDigest(config): config, contentDigest(payload): payload, contentDigest(signaturePayload): signaturePayload, root: manifestBytes, contentDigest(signatureBytes): signatureBytes}
	store, err := newOfflineRegistry(subjects, blobs)
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes[0] = 'x'
	subjects[0].Repository = "public.example.test/changed"
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://private.example.test/v2/distribution/manifests/"+root, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := store.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if contentDigest(data) != root {
		t.Fatal("store followed later caller mutations")
	}
	for _, location := range []string{"https://public.example.test/v2/distribution/manifests/" + root, "https://private.example.test/token", "http://private.example.test/v2/distribution/manifests/" + root, "https://private.example.test/v2/distribution/manifests/" + root + "?fallback=public"} {
		request, _ := http.NewRequest(http.MethodGet, location, nil)
		if _, err := store.RoundTrip(request); err == nil {
			t.Fatal("offline transport accepted external/alternate request")
		}
	}
	request, _ = http.NewRequest(http.MethodPost, "https://private.example.test/v2/distribution/blobs/uploads/", nil)
	if _, err := store.RoundTrip(request); err == nil {
		t.Fatal("offline store permitted writes")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.RoundTrip(request.WithContext(ctx)); err == nil {
		t.Fatal("canceled offline request continued")
	}
	for _, mutation := range []string{"config-url", "layer-url", "config-inline", "layer-inline"} {
		t.Run(mutation, func(t *testing.T) {
			changed := manifest
			changed.Layers = append([]ocispec.Descriptor(nil), manifest.Layers...)
			switch mutation {
			case "config-url":
				changed.Config.URLs = []string{"https://public.example.test/config"}
			case "layer-url":
				changed.Layers[0].URLs = []string{"https://public.example.test/payload"}
			case "config-inline":
				changed.Config.Data = []byte("inline")
			case "layer-inline":
				changed.Layers[0].Data = []byte("inline")
			}
			data, _ := json.Marshal(changed)
			copyGraph := map[string][]byte{}
			for key, data := range blobs {
				copyGraph[key] = data
			}
			delete(copyGraph, root)
			copyGraph[contentDigest(data)] = data
			changedSubject := StoredSubject{Repository: "private.example.test/distribution", Digest: contentDigest(data), SignatureDigest: contentDigest(signatureBytes)}
			if _, err := newOfflineRegistry([]StoredSubject{changedSubject}, copyGraph); err == nil || !strings.Contains(err.Error(), "descriptor") {
				t.Fatalf("external content descriptor accepted or failed unrelated check: %v", err)
			}
		})
	}
}
