//go:build integration

// Package testzot starts throwaway project-zot OCI registries for integration
// tests and pushes small synthetic artifacts into them.
package testzot

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/registry/remote"
)

// Image is the zot release the chart ships (registry.zot.image).
const Image = "ghcr.io/project-zot/zot:v2.1.22@sha256:96cda11459ce6f8c60da3b03f03e9f67fd11b3ae2d82c4c04c92b3c608996080"

// Start runs one zot registry (plain HTTP, anonymous read/write) and returns
// its host:port. The container is terminated when the test ends.
func Start(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        Image,
			ExposedPorts: []string{"5000/tcp"},
			WaitingFor:   wait.ForHTTP("/v2/").WithPort("5000/tcp").WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start zot: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("zot host: %v", err)
	}
	port, err := container.MappedPort(ctx, "5000/tcp")
	if err != nil {
		t.Fatalf("zot port: %v", err)
	}
	return fmt.Sprintf("%s:%s", host, port.Port())
}

// Repository returns a plain-HTTP oras repository handle.
func Repository(t *testing.T, ref string) *remote.Repository {
	t.Helper()
	repo, err := remote.NewRepository(ref)
	if err != nil {
		t.Fatalf("repository %s: %v", ref, err)
	}
	repo.PlainHTTP = true
	return repo
}

// PushImage pushes a tiny single-layer image manifest whose content is
// derived from seed and tags it. It returns the manifest descriptor.
func PushImage(t *testing.T, repo *remote.Repository, tag, seed string) ocispec.Descriptor {
	t.Helper()
	store := memory.New()
	desc := packImage(t, store, seed, nil)
	pushTagged(t, store, repo, desc, tag)
	return desc
}

// PushIndex pushes a two-platform image index (linux/amd64 + linux/arm64)
// tagged tag, and returns the index descriptor.
func PushIndex(t *testing.T, repo *remote.Repository, tag, seed string) ocispec.Descriptor {
	t.Helper()
	store := memory.New()
	amd := packImage(t, store, seed+"-amd64", &ocispec.Platform{OS: "linux", Architecture: "amd64"})
	arm := packImage(t, store, seed+"-arm64", &ocispec.Platform{OS: "linux", Architecture: "arm64"})
	index := ocispec.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex, Manifests: []ocispec.Descriptor{amd, arm}}
	desc := pushJSON(t, store, ocispec.MediaTypeImageIndex, index)
	pushTagged(t, store, repo, desc, tag)
	return desc
}

// PushReferrer attaches a cosign-style signature artifact to subject via the
// OCI referrers API and returns its descriptor.
func PushReferrer(t *testing.T, repo *remote.Repository, subject ocispec.Descriptor, seed string) ocispec.Descriptor {
	t.Helper()
	ctx := context.Background()
	layer := []byte("signature:" + seed)
	layerDesc := ocispec.Descriptor{MediaType: "application/vnd.dev.cosign.simplesigning.v1+json", Digest: digest.FromBytes(layer), Size: int64(len(layer))}
	if err := repo.Push(ctx, layerDesc, bytesReader(layer)); err != nil {
		t.Fatalf("push referrer layer: %v", err)
	}
	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest,
		ArtifactType: "application/vnd.dev.cosign.artifact.sig.v1+json",
		Config:       ocispec.DescriptorEmptyJSON,
		Layers:       []ocispec.Descriptor{layerDesc},
		Subject:      &subject,
	}
	if err := repo.Push(ctx, ocispec.DescriptorEmptyJSON, bytesReader(ocispec.DescriptorEmptyJSON.Data)); err != nil {
		t.Fatalf("push empty config: %v", err)
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	desc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromBytes(payload), Size: int64(len(payload)), ArtifactType: manifest.ArtifactType}
	if err := repo.Push(ctx, desc, bytesReader(payload)); err != nil {
		t.Fatalf("push referrer: %v", err)
	}
	return desc
}

func packImage(t *testing.T, store *memory.Store, seed string, platform *ocispec.Platform) ocispec.Descriptor {
	t.Helper()
	layer := []byte("layer:" + seed)
	layerDesc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageLayer, Digest: digest.FromBytes(layer), Size: int64(len(layer))}
	if err := store.Push(context.Background(), layerDesc, bytesReader(layer)); err != nil {
		t.Fatal(err)
	}
	cfg := ocispec.Image{Platform: ocispec.Platform{OS: "linux", Architecture: "amd64"}, RootFS: ocispec.RootFS{Type: "layers", DiffIDs: []digest.Digest{layerDesc.Digest}}}
	if platform != nil {
		cfg.Platform = *platform
	}
	cfgDesc := pushJSON(t, store, ocispec.MediaTypeImageConfig, cfg)
	manifest := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: cfgDesc, Layers: []ocispec.Descriptor{layerDesc}}
	desc := pushJSON(t, store, ocispec.MediaTypeImageManifest, manifest)
	desc.Platform = platform
	return desc
}

func pushJSON(t *testing.T, store *memory.Store, mediaType string, value any) ocispec.Descriptor {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	desc := ocispec.Descriptor{MediaType: mediaType, Digest: digest.FromBytes(payload), Size: int64(len(payload))}
	if err := store.Push(context.Background(), desc, bytesReader(payload)); err != nil {
		t.Fatal(err)
	}
	return desc
}

func pushTagged(t *testing.T, store *memory.Store, repo *remote.Repository, desc ocispec.Descriptor, tag string) {
	t.Helper()
	ctx := context.Background()
	if err := oras.CopyGraph(ctx, store, repo, desc, oras.DefaultCopyGraphOptions); err != nil {
		t.Fatalf("push %s: %v", tag, err)
	}
	if err := repo.Tag(ctx, desc, tag); err != nil {
		t.Fatalf("tag %s: %v", tag, err)
	}
}
