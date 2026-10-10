package resolver

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
	remoteauth "oras.land/oras-go/v2/registry/remote/auth"
)

// OfflineArtifactMetadata preserves unchanged manifest/config/legacy signature
// bytes for an operator transfer. It authenticates descriptors only and never
// transfers a large DB layer, accepts a decision or grants transport authority.
type OfflineArtifactMetadata struct {
	Metadata        ArtifactMetadata
	SignatureDigest string
	Blobs           map[string][]byte
}

// CaptureVerifiedOfflineArtifactMetadata reuses the same original canonical
// verifier before collecting any subject/config/transfer metadata. The guarded
// injected client remains beneath all ORAS requests and challenge handling.
func CaptureVerifiedOfflineArtifactMetadata(ctx context.Context, client *http.Client, repositoryName, subject, media string, policy model.TrustPolicy, trustDirectory string, maximum int64) (OfflineArtifactMetadata, error) {
	var result OfflineArtifactMetadata
	databaseLayer := media == "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip" || media == "application/vnd.aquasec.trivy.javadb.layer.v1.tar+gzip"
	if !databaseLayer && (media != "application/vnd.astronomer.vulnerability-db-update.v1+json" || maximum > 1<<20) {
		return result, errors.New("offline artifact transfer media or bound invalid")
	}
	metadata, verification, err := FetchVerifiedOCIArtifactMetadata(ctx, client, repositoryName, subject, media, policy, trustDirectory, maximum)
	if err != nil || verification.Status != "verified" {
		return result, errors.New("offline artifact canonical metadata authentication failed")
	}
	repository, err := remote.NewRepository(repositoryName)
	if err != nil || repository.Reference.Reference != "" {
		return result, errors.New("offline artifact repository invalid")
	}
	repository.Client = &remoteauth.Client{Client: client, Credential: remoteauth.StaticCredential(repository.Reference.Registry, remoteauth.EmptyCredential)}
	repository.MaxMetadataBytes = 1 << 20
	reference := "sha256-" + strings.TrimPrefix(subject, "sha256:") + ".sig"
	descriptor, reader, err := repository.FetchReference(ctx, reference)
	if err != nil {
		return result, errors.New("offline artifact original signature manifest unavailable")
	}
	signatureBytes, err := readBoundedAndClose(reader, descriptor.Size, 1<<20)
	if err != nil || descriptor.Digest.Validate() != nil || descriptor.Digest.Algorithm() != digest.SHA256 || digest.FromBytes(signatureBytes) != descriptor.Digest || len(descriptor.URLs) != 0 || len(descriptor.Data) != 0 {
		return result, errors.New("offline artifact signature manifest identity differs")
	}
	var manifest ocispec.Manifest
	if jsoncontract.Decode(signatureBytes, &manifest) != nil || manifest.SchemaVersion != 2 || manifest.MediaType != ocispec.MediaTypeImageManifest || manifest.Subject != nil || len(manifest.Layers) == 0 || len(manifest.Layers) > maxCosignSignatures {
		return result, errors.New("offline artifact original signature graph invalid")
	}
	blobs := map[string][]byte{subject: metadata.Manifest, digest.FromBytes(metadata.Config).String(): metadata.Config, descriptor.Digest.String(): signatureBytes}
	if !databaseLayer {
		reader, err := repository.Fetch(ctx, metadata.Layer)
		if err != nil {
			return result, errors.New("offline artifact original feed payload unavailable")
		}
		value, err := readBoundedAndClose(reader, metadata.Layer.Size, 1<<20)
		if err != nil || digest.FromBytes(value) != metadata.Layer.Digest {
			return result, errors.New("offline artifact original feed payload differs")
		}
		blobs[metadata.Layer.Digest.String()] = value
	}
	signatureConfig := append([]byte(nil), manifest.Config.Data...)
	if len(signatureConfig) == 0 {
		if manifest.Config.Digest.Validate() != nil || manifest.Config.Digest.Algorithm() != digest.SHA256 || manifest.Config.Size <= 0 || manifest.Config.Size > 1<<20 || len(manifest.Config.URLs) != 0 {
			return result, errors.New("offline artifact signature config descriptor invalid")
		}
		reader, err := repository.Fetch(ctx, manifest.Config)
		if err != nil {
			return result, errors.New("offline artifact signature config unavailable")
		}
		signatureConfig, err = readBoundedAndClose(reader, manifest.Config.Size, 1<<20)
		if err != nil {
			return result, err
		}
	}
	if !ArtifactConfigurationMatches(manifest.Config, signatureConfig) {
		return result, errors.New("offline artifact original signature config differs")
	}
	blobs[manifest.Config.Digest.String()] = signatureConfig
	for _, layer := range manifest.Layers {
		if layer.MediaType != cosignSimpleSigningMediaType || layer.Size <= 0 || layer.Size > 1<<20 || layer.Digest.Validate() != nil || layer.Digest.Algorithm() != digest.SHA256 || len(layer.URLs) != 0 || len(layer.Data) != 0 {
			return result, errors.New("offline artifact original signature layer descriptor invalid")
		}
		reader, err := repository.Fetch(ctx, layer)
		if err != nil {
			return result, errors.New("offline artifact signature original layer unavailable")
		}
		value, err := readBoundedAndClose(reader, layer.Size, 1<<20)
		if err != nil || digest.FromBytes(value) != layer.Digest {
			return result, errors.New("offline artifact signature layer differs")
		}
		blobs[layer.Digest.String()] = value
	}
	var aggregate int64
	for _, value := range blobs {
		aggregate += int64(len(value))
	}
	if len(blobs) > 24 || aggregate > 8<<20 {
		return result, errors.New("offline artifact transfer metadata exceeds finite count/byte bounds")
	}
	return OfflineArtifactMetadata{Metadata: metadata, SignatureDigest: descriptor.Digest.String(), Blobs: blobs}, nil
}
