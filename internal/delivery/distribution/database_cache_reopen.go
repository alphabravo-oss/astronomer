package distribution

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/google/uuid"
)

// OpenSealedDatabaseCache reopens only the helper's literal sealed members for
// bounded read-only measurement. Expected fields are comparison DATA from the
// prepare process. This does not authenticate a feed, grant acceptance, prove a
// scanner used the files, or infer Kubernetes/process identities. The worker
// must separately bind actual init/final logs and runtime identities to current
// authenticated relational intent before recording any witness.
func OpenSealedDatabaseCache(ctx context.Context, cacheRoot string, claimID uuid.UUID, expected DatabaseCacheSnapshot, mainMetadata, javaMetadata DatabaseUpdateMetadata) (*MaterializedDatabaseCache, error) {
	if ctx == nil || !filepath.IsAbs(cacheRoot) || filepath.Clean(cacheRoot) != cacheRoot || claimID.Version() != 4 || claimID.Variant() != uuid.RFC4122 || !digestPattern.MatchString(expected.FeedDigest) {
		return nil, errors.New("database cache measurement inputs invalid")
	}
	var size int64
	for index, content := range []DatabaseCacheContent{expected.Main, expected.Java} {
		role, name, metadata := "trivy-database", "trivy.db", mainMetadata
		if index == 1 {
			role, name, metadata = "trivy-java-database", "trivy-java.db", javaMetadata
		}
		if content.Role != role || content.File != name || !digestPattern.MatchString(content.Digest) || content.Size <= 0 || content.Size > MaximumDatabaseExpandedBytes-size || !digestPattern.MatchString(metadata.Digest) || metadata.Size <= 0 || metadata.Size > 64<<10 {
			return nil, errors.New("database cache measurement comparison invalid")
		}
		size += content.Size
		if metadata.Size > MaximumDatabaseExpandedBytes-size {
			return nil, errors.New("database cache measurement aggregate invalid")
		}
		size += metadata.Size
	}
	return openSealedDatabaseCache(ctx, cacheRoot, "database-cache-"+claimID.String(), expected, mainMetadata, javaMetadata)
}
