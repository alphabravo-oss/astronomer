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

// ResolveDatabaseUpdateCurrent returns retrieval DATA only. The discovery tag
// is fixed by this process adapter; it supplies no trust, lifetime, network or
// mutation authority. The consumer must authenticate this exact immutable
// digest through its existing DatabaseAuthority before fetching payloads.
func ResolveDatabaseUpdateCurrent(ctx context.Context, client *http.Client, repositoryName string) (string, error) {
	if ctx == nil || client == nil {
		return "", errors.New("database update discovery context unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	repository, err := remote.NewRepository(repositoryName)
	if err != nil || repository.Reference.Reference != "" {
		return "", errors.New("database update discovery repository invalid")
	}
	repository.Client = &remoteauth.Client{Client: client, Credential: remoteauth.StaticCredential(repository.Reference.Registry, remoteauth.EmptyCredential)}
	repository.MaxMetadataBytes = 1 << 20
	descriptor, err := repository.Resolve(ctx, "current")
	if err != nil || descriptor.Digest.Validate() != nil || descriptor.Digest.Algorithm() != digest.SHA256 || descriptor.MediaType != ocispec.MediaTypeImageManifest || descriptor.Size <= 0 || descriptor.Size > 1<<20 || len(descriptor.URLs) != 0 || len(descriptor.Data) != 0 {
		return "", supplierMetadataError(ctx, "database update discovery immutable descriptor unavailable")
	}
	return descriptor.Digest.String(), nil
}
