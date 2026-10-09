package main

import (
	"context"
	"errors"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
)

// The copy keeps every platform and referenced blob; it never selects only the
// host architecture. Bounds are intentionally finite, and an oversized supplier
// image blocks qualification instead of silently dropping layers/platforms.
type imageGraph struct {
	repository  *remote.Repository
	descriptors map[string]ocispec.Descriptor
	total       int64
}

func preflightImageGraph(ctx context.Context, repository *remote.Repository, subject string) (imageGraph, error) {
	graph := imageGraph{repository: repository, descriptors: map[string]ocispec.Descriptor{}}
	descriptor, err := repository.Resolve(ctx, subject)
	if err != nil || descriptor.Digest.String() != subject || !imageIndexType(descriptor.MediaType) {
		return graph, errors.New("immutable multiarch mirror image unavailable")
	}
	if err := graph.visit(ctx, descriptor, 0); err != nil {
		return graph, err
	}
	return graph, nil
}
func (graph *imageGraph) retain(descriptor ocispec.Descriptor, maximum int64) error {
	if descriptor.Digest.Algorithm() != digest.SHA256 || descriptor.Size <= 0 || descriptor.Size > maximum || len(descriptor.URLs) != 0 || len(descriptor.Data) != 0 {
		return errors.New("image graph has external, missing or oversized descriptor")
	}
	if existing, ok := graph.descriptors[descriptor.Digest.String()]; ok {
		if existing.Size != descriptor.Size || existing.MediaType != descriptor.MediaType {
			return errors.New("image descriptor identity contradicts another member")
		}
		return nil
	}
	if len(graph.descriptors) >= 4096 || graph.total+descriptor.Size > 2*distribution.MaximumExpandedBytes {
		return errors.New("multiarch image graph exceeds finite bounds")
	}
	graph.total += descriptor.Size
	graph.descriptors[descriptor.Digest.String()] = descriptor
	return nil
}
func (graph *imageGraph) visit(ctx context.Context, descriptor ocispec.Descriptor, depth int) error {
	if depth > 8 {
		return errors.New("image index recursion exceeds bound")
	}
	if _, seen := graph.descriptors[descriptor.Digest.String()]; seen {
		return graph.retain(descriptor, 1<<20)
	}
	if err := graph.retain(descriptor, 1<<20); err != nil {
		return err
	}
	reader, err := graph.repository.Fetch(ctx, descriptor)
	if err != nil {
		return errors.New("mirror image metadata unavailable")
	}
	data, err := captureBytes(reader, descriptor, 1<<20)
	if err != nil {
		return err
	}
	if imageIndexType(descriptor.MediaType) {
		var index ocispec.Index
		if closedJSON(data, &index) != nil || index.SchemaVersion != 2 || len(index.Manifests) == 0 || len(index.Manifests) > 64 || index.Subject != nil {
			return errors.New("mirror image index invalid")
		}
		required := map[string]bool{}
		for _, child := range index.Manifests {
			if child.Platform != nil && child.Platform.OS == "linux" {
				required[child.Platform.Architecture] = true
			}
			if !imageIndexType(child.MediaType) && !imageManifestType(child.MediaType) {
				return errors.New("image child media type unqualified")
			}
			if err := graph.visit(ctx, child, depth+1); err != nil {
				return err
			}
		}
		if depth == 0 && (!required["amd64"] || !required["arm64"]) {
			return errors.New("mirror image lacks qualified architectures")
		}
		return nil
	}
	if !imageManifestType(descriptor.MediaType) {
		return errors.New("image manifest type unsupported")
	}
	var manifest ocispec.Manifest
	if closedJSON(data, &manifest) != nil || manifest.SchemaVersion != 2 || len(manifest.Layers) == 0 || len(manifest.Layers) > 256 || manifest.Subject != nil {
		return errors.New("mirror image manifest invalid")
	}
	if err := graph.retain(manifest.Config, 1<<20); err != nil {
		return err
	}
	reader, err = graph.repository.Fetch(ctx, manifest.Config)
	if err != nil {
		return errors.New("mirror image config unavailable")
	}
	config, err := captureBytes(reader, manifest.Config, 1<<20)
	if err != nil {
		return err
	}
	// Docker/OCI image configuration has additive supplier-owned fields; the map
	// preserves that schema while the canonical reader rejects ambiguity/depth.
	var configuration map[string]any
	if closedJSON(config, &configuration) != nil || configuration == nil {
		return errors.New("mirror image configuration invalid")
	}
	if descriptor.Platform != nil && descriptor.Platform.OS == "linux" && (descriptor.Platform.Architecture == "amd64" || descriptor.Platform.Architecture == "arm64") {
		if configuration["os"] != "linux" || configuration["architecture"] != descriptor.Platform.Architecture {
			return errors.New("mirror image configuration contradicts qualified platform")
		}
	}
	for _, layer := range manifest.Layers {
		if err := graph.retain(layer, distribution.MaximumArchiveBytes); err != nil {
			return err
		}
		exists, err := graph.repository.Exists(ctx, layer)
		if err != nil || !exists {
			return errors.New("mirror image layer unavailable before copy")
		}
	}
	return nil
}
func (graph imageGraph) checkDescriptor(_ context.Context, descriptor ocispec.Descriptor) error {
	expected, ok := graph.descriptors[descriptor.Digest.String()]
	if !ok || descriptor.Size != expected.Size || descriptor.MediaType != expected.MediaType || len(descriptor.URLs) != 0 || len(descriptor.Data) != 0 {
		return errors.New("copy traversed unqualified image descriptor")
	}
	return nil
}
func imageIndexType(media string) bool {
	return media == ocispec.MediaTypeImageIndex || media == "application/vnd.docker.distribution.manifest.list.v2+json"
}
func imageManifestType(media string) bool {
	return media == ocispec.MediaTypeImageManifest || media == "application/vnd.docker.distribution.manifest.v2+json"
}
