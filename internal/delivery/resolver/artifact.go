package resolver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
	remoteauth "oras.land/oras-go/v2/registry/remote/auth"
)

// FetchVerifiedOCIArtifact authenticates the pinned subject using the canonical
// offline verifier before fetching its single bounded payload. Descriptor URLs
// never grant alternate network locations. Credentials belong in the injected
// guarded client; they are never part of repository identities or diagnostics.
func FetchVerifiedOCIArtifact(ctx context.Context, client *http.Client, repositoryName, subjectDigest, mediaType string, policy model.TrustPolicy, trustDirectory string, maximum int64) ([]byte, Verification, error) {
	verification, err := VerifyPinnedOCI(ctx, client, repositoryName, subjectDigest, policy, trustDirectory)
	if err != nil {
		return nil, Verification{}, err
	}
	payload, err := fetchArtifactPayload(ctx, client, repositoryName, subjectDigest, mediaType, maximum)
	if err != nil {
		return nil, Verification{}, err
	}
	return payload, verification, nil
}

func fetchArtifactPayload(ctx context.Context, client *http.Client, repositoryName, subjectDigest, mediaType string, maximum int64) ([]byte, error) {
	if maximum <= 0 || maximum > 512<<20 {
		return nil, errors.New("OCI artifact size policy is invalid")
	}
	repository, err := remote.NewRepository(repositoryName)
	if err != nil {
		return nil, errors.New("OCI artifact repository is invalid")
	}
	repository.Client = &remoteauth.Client{Client: client, Credential: remoteauth.StaticCredential(repository.Reference.Registry, remoteauth.EmptyCredential)}
	repository.MaxMetadataBytes = 1 << 20
	descriptor, reader, err := repository.FetchReference(ctx, subjectDigest)
	if err != nil {
		return nil, errors.New("OCI artifact manifest is unavailable")
	}
	manifestBytes, err := readArtifactPayload(reader, descriptor.Size, 1<<20)
	_ = reader.Close()
	if err != nil {
		return nil, err
	}
	if descriptor.Digest.String() != subjectDigest || digest.FromBytes(manifestBytes) != descriptor.Digest {
		return nil, errors.New("OCI artifact manifest digest mismatch")
	}
	var manifest ocispec.Manifest
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil || manifest.SchemaVersion != 2 || len(manifest.Layers) != 1 {
		return nil, errors.New("OCI artifact must have one payload layer")
	}
	layer := manifest.Layers[0]
	if layer.MediaType != mediaType || len(layer.URLs) != 0 || layer.Digest.Algorithm() != digest.SHA256 {
		return nil, errors.New("OCI artifact payload descriptor is invalid")
	}
	if layer.Size <= 0 || layer.Size > maximum {
		return nil, errors.New("OCI artifact payload exceeds size limit")
	}
	payloadReader, err := repository.Fetch(ctx, layer)
	if err != nil {
		return nil, errors.New("OCI artifact payload is unavailable")
	}
	payload, err := readArtifactPayload(payloadReader, layer.Size, maximum)
	_ = payloadReader.Close()
	if err != nil {
		return nil, err
	}
	if digest.FromBytes(payload) != layer.Digest {
		return nil, errors.New("OCI artifact payload digest mismatch")
	}
	return payload, nil
}

func readArtifactPayload(reader io.Reader, declared, maximum int64) ([]byte, error) {
	if declared <= 0 || declared > maximum {
		return nil, errors.New("OCI artifact declared size exceeds bounds")
	}
	result, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil || int64(len(result)) != declared || int64(len(result)) > maximum {
		return nil, errors.New("OCI artifact payload size mismatch")
	}
	return result, nil
}
