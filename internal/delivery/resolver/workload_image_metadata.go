package resolver

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
)

// WorkloadImageMetadata is immutable content DATA. A workload is not a vendor
// distribution artifact; its authority must come from the actual current Pod
// imageID and locked target identity, not this parser or an image annotation.
// Config may contain private application defaults: never print or persist its
// bytes in public status/logs. The owned preparation keeps only private files.
type WorkloadImageMetadata struct {
	Manifest, Config   []byte
	ManifestDescriptor ocispec.Descriptor
	ConfigDescriptor   ocispec.Descriptor
	Layers             []ocispec.Descriptor
}

func FetchWorkloadImageMetadata(ctx context.Context, client *http.Client, repositoryName, subject, architecture string) (WorkloadImageMetadata, error) {
	var empty WorkloadImageMetadata
	selected := digest.Digest(subject)
	if ctx == nil || client == nil || selected.Validate() != nil || selected.Algorithm() != digest.SHA256 || (architecture != "amd64" && architecture != "arm64") {
		return empty, errors.New("workload image immutable/platform inputs invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	repository, err := remote.NewRepository(repositoryName)
	if err != nil || repository.Reference.Reference != "" {
		return empty, errors.New("workload image repository invalid")
	}
	guarded, err := databaseLayerRegistryClient(client, repositoryName)
	if err != nil {
		return empty, err
	}
	repository.Client = guarded
	repository.MaxMetadataBytes = 1 << 20
	descriptor, reader, err := repository.FetchReference(ctx, subject)
	if err != nil {
		return empty, supplierMetadataError(ctx, "workload immutable manifest unavailable")
	}
	manifestBytes, err := readArtifactPayload(reader, descriptor.Size, 1<<20)
	_ = reader.Close()
	if err != nil || descriptor.Digest != selected || digest.FromBytes(manifestBytes) != selected || len(descriptor.URLs) != 0 || len(descriptor.Data) != 0 {
		return empty, supplierMetadataError(ctx, "workload immutable manifest binding invalid")
	}
	manifest, err := validateWorkloadManifest(manifestBytes, subject)
	if err != nil || descriptor.MediaType != manifest.MediaType {
		return empty, supplierMetadataError(ctx, "workload immutable manifest shape invalid")
	}
	config := manifest.Config
	configBytes := bytes.Clone(config.Data)
	if len(config.Data) == 0 {
		input, err := repository.Fetch(ctx, config)
		if err != nil {
			return empty, supplierMetadataError(ctx, "workload image config unavailable")
		}
		configBytes, err = readArtifactPayload(input, config.Size, 4<<20)
		_ = input.Close()
		if err != nil {
			return empty, supplierMetadataError(ctx, "workload image config bytes unavailable")
		}
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	return ValidateWorkloadImageMetadata(manifestBytes, configBytes, subject, architecture)
}

// ValidateWorkloadImageMetadata is the same bounded hash-before-parse core used
// by the guarded fetch and owned local layout readback. This validates content
// DATA only; caller-created bytes cannot grant current workload/scan authority.
func ValidateWorkloadImageMetadata(manifestBytes, configBytes []byte, subject, architecture string) (WorkloadImageMetadata, error) {
	var empty WorkloadImageMetadata
	if architecture != "amd64" && architecture != "arm64" {
		return empty, errors.New("workload platform unsupported")
	}
	manifest, err := validateWorkloadManifest(manifestBytes, subject)
	if err != nil {
		return empty, err
	}
	config := manifest.Config
	if len(configBytes) == 0 || len(configBytes) > 4<<20 || int64(len(configBytes)) != config.Size || digest.FromBytes(configBytes) != config.Digest || len(config.Data) > 0 && !bytes.Equal(config.Data, configBytes) {
		return empty, errors.New("workload config exact byte binding invalid")
	}
	var image ocispec.Image
	if jsoncontract.Decode(configBytes, &image) != nil || image.OS != "linux" || image.Architecture != architecture || image.RootFS.Type != "layers" || len(image.RootFS.DiffIDs) != len(manifest.Layers) {
		return empty, errors.New("workload selected platform/config schema unsupported")
	}
	for _, diff := range image.RootFS.DiffIDs {
		if diff.Validate() != nil || diff.Algorithm() != digest.SHA256 {
			return empty, errors.New("workload rootfs descriptor invalid")
		}
	}
	layers := make([]ocispec.Descriptor, len(manifest.Layers))
	for index, layer := range manifest.Layers {
		layers[index] = ocispec.Descriptor{Digest: layer.Digest, Size: layer.Size, MediaType: layer.MediaType}
	}
	return WorkloadImageMetadata{Manifest: bytes.Clone(manifestBytes), Config: bytes.Clone(configBytes), ManifestDescriptor: ocispec.Descriptor{Digest: digest.Digest(subject), Size: int64(len(manifestBytes)), MediaType: manifest.MediaType}, ConfigDescriptor: ocispec.Descriptor{Digest: config.Digest, Size: config.Size, MediaType: config.MediaType}, Layers: layers}, nil
}
func validateWorkloadManifest(manifestBytes []byte, subject string) (ocispec.Manifest, error) {
	selected := digest.Digest(subject)
	if selected.Validate() != nil || selected.Algorithm() != digest.SHA256 || len(manifestBytes) == 0 || len(manifestBytes) > 1<<20 || digest.FromBytes(manifestBytes) != selected {
		return ocispec.Manifest{}, errors.New("workload whole manifest hash invalid")
	}
	var manifest ocispec.Manifest
	if jsoncontract.Decode(manifestBytes, &manifest) != nil || manifest.SchemaVersion != 2 || (manifest.MediaType != ocispec.MediaTypeImageManifest && manifest.MediaType != "application/vnd.docker.distribution.manifest.v2+json") || manifest.ArtifactType != "" || manifest.Subject != nil || len(manifest.Layers) > 32 {
		return ocispec.Manifest{}, errors.New("workload image manifest shape unsupported")
	}
	config := manifest.Config
	if config.Digest.Validate() != nil || config.Digest.Algorithm() != digest.SHA256 || config.Size <= 0 || config.Size > 4<<20 || len(config.URLs) != 0 || (config.MediaType != ocispec.MediaTypeImageConfig && config.MediaType != "application/vnd.docker.container.image.v1+json") {
		return ocispec.Manifest{}, errors.New("workload image config descriptor unsupported")
	}
	var aggregate int64 = config.Size + int64(len(manifestBytes))
	seen := map[digest.Digest]ocispec.Descriptor{}
	for _, layer := range manifest.Layers {
		if layer.Digest.Validate() != nil || layer.Digest.Algorithm() != digest.SHA256 || layer.Size <= 0 || layer.Size > 2<<30 || len(layer.URLs) != 0 || len(layer.Data) != 0 || !workloadImageLayerMedia(layer.MediaType) || layer.Size > (8<<30)-aggregate {
			return ocispec.Manifest{}, errors.New("workload image layer closure invalid")
		}
		aggregate += layer.Size
		if previous, ok := seen[layer.Digest]; ok && (previous.Size != layer.Size || previous.MediaType != layer.MediaType) {
			return ocispec.Manifest{}, errors.New("workload image duplicate layer descriptors differ")
		}
		seen[layer.Digest] = layer
	}
	return manifest, nil
}

func workloadImageLayerMedia(media string) bool {
	switch media {
	case ocispec.MediaTypeImageLayer, ocispec.MediaTypeImageLayerGzip, ocispec.MediaTypeImageLayerZstd, "application/vnd.docker.image.rootfs.diff.tar.gzip":
		return true
	default:
		return false
	}
}
