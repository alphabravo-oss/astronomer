package resolver

import (
	"context"
	"errors"
	"net/http"
	"time"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
	remoteauth "oras.land/oras-go/v2/registry/remote/auth"
)

// CaptureSupplierDatabaseMetadata is for the operator's new-upstream update
// producer, never the signed runtime consumer. The constructor that composes it
// must own the reviewed repository and guarded client/destination policy. There
// is no supplier signature claim: TLS transport and exact immutable content
// hashes are its limited source-authentication scope. It fetches no layer bytes.
// A schema tag is resolved once; all later reads use the captured digest.
func CaptureSupplierDatabaseMetadata(ctx context.Context, client *http.Client, repositoryName, schemaTag, mediaType string) (string, ArtifactMetadata, error) {
	var empty ArtifactMetadata
	if ctx == nil || client == nil || ((schemaTag != "2" || mediaType != "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip") && (schemaTag != "1" || mediaType != "application/vnd.aquasec.trivy.javadb.layer.v1.tar+gzip")) {
		return "", empty, errors.New("supplier database capture inputs invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	repository, err := remote.NewRepository(repositoryName)
	if err != nil || repository.Reference.Reference != "" {
		return "", empty, errors.New("supplier database repository invalid")
	}
	repository.Client = &remoteauth.Client{Client: client, Credential: remoteauth.StaticCredential(repository.Reference.Registry, remoteauth.EmptyCredential)}
	repository.MaxMetadataBytes = 1 << 20
	if err := ctx.Err(); err != nil {
		return "", empty, err
	}
	descriptor, err := repository.Resolve(ctx, schemaTag)
	if err != nil || descriptor.Digest.Validate() != nil || descriptor.Digest.Algorithm() != digest.SHA256 || descriptor.MediaType != ocispec.MediaTypeImageManifest || descriptor.Size <= 0 || descriptor.Size > 1<<20 || len(descriptor.URLs) != 0 || len(descriptor.Data) != 0 {
		return "", empty, supplierMetadataError(ctx, "supplier database immutable resolution failed")
	}
	subject := descriptor.Digest.String()
	metadata, err := fetchArtifactMetadata(ctx, client, repositoryName, subject, mediaType, 2<<30)
	if err != nil || int64(len(metadata.Manifest)) != descriptor.Size {
		return "", empty, supplierMetadataError(ctx, "supplier database captured metadata binding failed")
	}
	return subject, metadata, nil
}

func supplierMetadataError(ctx context.Context, message string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New(message)
}
