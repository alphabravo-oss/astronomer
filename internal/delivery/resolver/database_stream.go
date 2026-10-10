package resolver

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
)

const databaseRangeBytes int64 = 64 << 20

// StreamVerifiedOCIDatabase authenticates an exact database child before any
// payload request. Finite range requests retain the caller's canonical response
// bounds and destination/credential controls. A writer must be an owned
// quarantine: bytes become accepted only after this function succeeds. Passing
// nil verifies without retaining payload bytes. This does not assess freshness.
func StreamVerifiedOCIDatabase(ctx context.Context, client *http.Client, repositoryName, subjectDigest string, expected ocispec.Descriptor, destination io.Writer, policy model.TrustPolicy, trustDirectory string) (Verification, error) {
	if ctx == nil || client == nil || expected.Digest.Validate() != nil || expected.Size <= 0 || expected.Size > 2<<30 || expected.Digest.Algorithm() != digest.SHA256 || len(expected.URLs) != 0 || len(expected.Data) != 0 || (expected.MediaType != "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip" && expected.MediaType != "application/vnd.aquasec.trivy.javadb.layer.v1.tar+gzip") {
		return Verification{}, errors.New("streamed database claim invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Verification{}, err
	}
	metadata, verification, err := FetchVerifiedOCIArtifactMetadata(ctx, client, repositoryName, subjectDigest, expected.MediaType, policy, trustDirectory, 2<<30)
	if err != nil || metadata.Layer.Digest != expected.Digest || metadata.Layer.Size != expected.Size || metadata.Layer.MediaType != expected.MediaType {
		return Verification{}, supplierMetadataError(ctx, "streamed database signature or descriptor differs from expected identity")
	}
	if err := streamPinnedDatabaseLayer(ctx, client, repositoryName, expected, destination); err != nil {
		return Verification{}, err
	}
	return verification, nil
}

// StreamSupplierDatabaseLayer is an operator producer content-transfer boundary,
// not a signature verifier. Compose it only after the constructor-owned reviewed
// origin capture has pinned these descriptors. It cannot return Verification or
// an authenticated consumer update. The signed consumer separately verifies its
// original vendor signature before entering the same finite exact-byte core.
func StreamSupplierDatabaseLayer(ctx context.Context, client *http.Client, repositoryName string, expected ocispec.Descriptor, destination io.Writer) error {
	return streamPinnedDatabaseLayer(ctx, client, repositoryName, expected, destination)
}

func streamPinnedDatabaseLayer(ctx context.Context, client *http.Client, repositoryName string, expected ocispec.Descriptor, destination io.Writer) error {
	if ctx == nil || client == nil || expected.Digest.Validate() != nil || expected.Digest.Algorithm() != digest.SHA256 || expected.Size <= 0 || expected.Size > 2<<30 || len(expected.URLs) != 0 || len(expected.Data) != 0 || (expected.MediaType != "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip" && expected.MediaType != "application/vnd.aquasec.trivy.javadb.layer.v1.tar+gzip") {
		return errors.New("streamed database claim invalid")
	}
	return streamPinnedOCILayer(ctx, client, repositoryName, expected, destination)
}

// StreamWorkloadImageLayer is a complete immutable content check only. The
// owned worker must independently bind the source manifest to the actual
// current workload image ID. This is not a signature or DB freshness verifier.
func StreamWorkloadImageLayer(ctx context.Context, client *http.Client, repositoryName string, expected ocispec.Descriptor, destination io.Writer) error {
	if !workloadImageLayerMedia(expected.MediaType) {
		return errors.New("workload image layer media unsupported")
	}
	return streamPinnedOCILayer(ctx, client, repositoryName, expected, destination)
}

// Shared exact Range/auth/digest core; DB wrappers retain their narrower media
// and signature rules. No public constructor can bypass descriptor bounds.
func streamPinnedOCILayer(ctx context.Context, client *http.Client, repositoryName string, expected ocispec.Descriptor, destination io.Writer) error {
	if ctx == nil || client == nil || expected.Digest.Validate() != nil || expected.Digest.Algorithm() != digest.SHA256 || expected.Size <= 0 || expected.Size > 2<<30 || len(expected.URLs) != 0 || len(expected.Data) != 0 {
		return errors.New("streamed immutable OCI blob claim invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	repository, err := remote.NewRepository(repositoryName)
	if err != nil || repository.Reference.Reference != "" {
		return errors.New("streamed database repository invalid")
	}
	client, err = databaseLayerRegistryClient(client, repositoryName)
	if err != nil {
		return err
	}
	location := "https://" + repository.Reference.Registry + "/v2/" + repository.Reference.Repository + "/blobs/" + expected.Digest.String()
	if destination == nil {
		destination = io.Discard
	}
	hash := sha256.New()
	writer := io.MultiWriter(destination, hash)
	buffer := make([]byte, 64<<10)
	for offset := int64(0); offset < expected.Size; offset += databaseRangeBytes {
		end := min(offset+databaseRangeBytes, expected.Size) - 1
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
		if err != nil {
			return errors.New("streamed database request invalid")
		}
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, end))
		request.Header.Set("Accept-Encoding", "identity")
		response, err := client.Do(request)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return context.Canceled
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return context.DeadlineExceeded
			}
			return supplierMetadataError(ctx, "streamed database range unavailable")
		}
		if err := consumeDatabaseRange(response, writer, buffer, offset, end, expected.Size); err != nil {
			return supplierMetadataError(ctx, err.Error())
		}
	}
	if fmt.Sprintf("sha256:%x", hash.Sum(nil)) != expected.Digest.String() {
		return errors.New("streamed database complete payload digest mismatch")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func consumeDatabaseRange(response *http.Response, writer io.Writer, buffer []byte, start, end, size int64) error {
	defer response.Body.Close()
	length := end - start + 1
	if response.StatusCode != http.StatusPartialContent || len(response.Header.Values("Content-Range")) != 1 || response.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", start, end, size) || strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "multipart/") || response.Uncompressed || (response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity") || (response.ContentLength >= 0 && response.ContentLength != length) {
		return errors.New("streamed database range response differs from exact bounds")
	}
	written, err := io.CopyBuffer(writer, io.LimitReader(response.Body, length+1), buffer)
	if err != nil || written != length {
		return errors.New("streamed database range partial or exceeds declared bytes")
	}
	return nil
}

func databaseLayerRegistryClient(client *http.Client, repository string) (*http.Client, error) {
	if authenticated, ok := client.Transport.(*databaseAuthenticatedTransport); ok {
		parsed, err := remote.NewRepository(repository)
		if err != nil || authenticated.host != parsed.Reference.Registry || authenticated.repository != parsed.Reference.Repository {
			return nil, errors.New("database registry client scope differs")
		}
		return client, nil
	}
	return NewDatabaseRegistryClient(client, repository, DatabaseRegistryAuthentication{})
}
