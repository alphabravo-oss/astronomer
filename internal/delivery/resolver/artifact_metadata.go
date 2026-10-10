package resolver

import (
	"context"
	"errors"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
	remoteauth "oras.land/oras-go/v2/registry/remote/auth"
)

// ArtifactMetadata authenticates descriptor claims only. It does not assert
// payload availability, hash verification, database freshness or qualification.
type ArtifactMetadata struct {
	Manifest []byte
	Config   []byte
	Layer    ocispec.Descriptor
}

// FetchVerifiedOCIArtifactMetadata uses the same signature-first verifier for
// large database children without allocating their payload. Its separate finite
// descriptor ceiling does not expand FetchVerifiedOCIArtifact's payload caps.
func FetchVerifiedOCIArtifactMetadata(ctx context.Context, client *http.Client, repositoryName, subjectDigest, mediaType string, policy model.TrustPolicy, trustDirectory string, maximumLayer int64) (ArtifactMetadata, Verification, error) {
	var empty ArtifactMetadata
	if maximumLayer <= 0 || maximumLayer > 2<<30 {
		return empty, Verification{}, errors.New("OCI metadata layer bound invalid")
	}
	verification, err := VerifyPinnedOCI(ctx, client, repositoryName, subjectDigest, policy, trustDirectory)
	if err != nil {
		return empty, Verification{}, err
	}
	metadata, err := fetchArtifactMetadata(ctx, client, repositoryName, subjectDigest, mediaType, maximumLayer)
	if err != nil {
		return empty, Verification{}, err
	}
	return metadata, verification, nil
}

// fetchArtifactMetadata is a shared bounded content parser. Signature-first
// consumers establish vendor trust before reaching it; the separate operator
// supplier capture establishes only reviewed transport and immutable bytes.
func fetchArtifactMetadata(ctx context.Context, client *http.Client, repositoryName, subjectDigest, mediaType string, maximumLayer int64) (ArtifactMetadata, error) {
	var empty ArtifactMetadata
	repository, manifest, manifestBytes, err := fetchArtifactManifest(ctx, client, repositoryName, subjectDigest)
	if err != nil {
		return empty, err
	}
	layer := manifest.Layers[0]
	config := manifest.Config
	if manifest.Subject != nil || layer.MediaType != mediaType || layer.Digest.Algorithm() != digest.SHA256 || layer.Size <= 0 || layer.Size > maximumLayer || len(layer.URLs) != 0 || len(layer.Data) != 0 || config.Digest.Algorithm() != digest.SHA256 || config.Size <= 0 || config.Size > 1<<20 || len(config.URLs) != 0 || len(config.Data) != 0 {
		return empty, errors.New("OCI metadata descriptor invalid")
	}
	reader, err := repository.Fetch(ctx, config)
	if err != nil {
		return empty, errors.New("OCI artifact configuration unavailable")
	}
	configBytes, err := readArtifactPayload(reader, config.Size, 1<<20)
	_ = reader.Close()
	if err != nil || digest.FromBytes(configBytes) != config.Digest {
		return empty, errors.New("OCI artifact configuration binding invalid")
	}
	var object map[string]any
	if jsoncontract.Decode(configBytes, &object) != nil || object == nil {
		return empty, errors.New("OCI artifact configuration ambiguous or invalid")
	}
	return ArtifactMetadata{Manifest: manifestBytes, Config: configBytes, Layer: layer}, nil
}

func fetchArtifactManifest(ctx context.Context, client *http.Client, repositoryName, subjectDigest string) (*remote.Repository, ocispec.Manifest, []byte, error) {
	var empty ocispec.Manifest
	repository, err := remote.NewRepository(repositoryName)
	if err != nil {
		return nil, empty, nil, errors.New("OCI artifact repository invalid")
	}
	repository.Client = &remoteauth.Client{Client: client, Credential: remoteauth.StaticCredential(repository.Reference.Registry, remoteauth.EmptyCredential)}
	repository.MaxMetadataBytes = 1 << 20
	descriptor, reader, err := repository.FetchReference(ctx, subjectDigest)
	if err != nil {
		return nil, empty, nil, errors.New("OCI artifact manifest unavailable")
	}
	manifestBytes, err := readArtifactPayload(reader, descriptor.Size, 1<<20)
	_ = reader.Close()
	if err != nil {
		return nil, empty, nil, err
	}
	if descriptor.Digest.String() != subjectDigest || digest.FromBytes(manifestBytes) != descriptor.Digest || len(descriptor.URLs) != 0 || len(descriptor.Data) != 0 {
		return nil, empty, nil, errors.New("OCI artifact manifest digest mismatch")
	}
	var manifest ocispec.Manifest
	if jsoncontract.Decode(manifestBytes, &manifest) != nil || manifest.SchemaVersion != 2 || len(manifest.Layers) != 1 {
		return nil, empty, nil, errors.New("OCI artifact must have one unambiguous payload layer")
	}
	return repository, manifest, manifestBytes, nil
}
