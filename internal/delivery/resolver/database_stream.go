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
	if client == nil || expected.Size <= 0 || expected.Size > 2<<30 || expected.Digest.Algorithm() != digest.SHA256 || len(expected.URLs) != 0 || len(expected.Data) != 0 || (expected.MediaType != "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip" && expected.MediaType != "application/vnd.aquasec.trivy.javadb.layer.v1.tar+gzip") {
		return Verification{}, errors.New("streamed database claim invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if ctx.Err() != nil {
		return Verification{}, errors.New("streamed database operation cancelled")
	}
	metadata, verification, err := FetchVerifiedOCIArtifactMetadata(ctx, client, repositoryName, subjectDigest, expected.MediaType, policy, trustDirectory, 2<<30)
	if err != nil || metadata.Layer.Digest != expected.Digest || metadata.Layer.Size != expected.Size || metadata.Layer.MediaType != expected.MediaType {
		return Verification{}, errors.New("streamed database signature or descriptor differs from expected identity")
	}
	repository, err := remote.NewRepository(repositoryName)
	if err != nil || repository.Reference.Reference != "" {
		return Verification{}, errors.New("streamed database repository invalid")
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
			return Verification{}, errors.New("streamed database request invalid")
		}
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, end))
		request.Header.Set("Accept-Encoding", "identity")
		response, err := client.Do(request)
		if err != nil {
			return Verification{}, errors.New("streamed database range unavailable")
		}
		if err := consumeDatabaseRange(response, writer, buffer, offset, end, expected.Size); err != nil {
			return Verification{}, err
		}
	}
	if fmt.Sprintf("sha256:%x", hash.Sum(nil)) != expected.Digest.String() {
		return Verification{}, errors.New("streamed database complete payload digest mismatch")
	}
	return verification, nil
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
