//go:build !unix

package distribution

import (
	"context"
	"errors"
)

func openSealedDatabaseCache(context.Context, string, string, DatabaseCacheSnapshot, DatabaseUpdateMetadata, DatabaseUpdateMetadata) (*MaterializedDatabaseCache, error) {
	return nil, errors.New("owned scan cache remeasurement is not qualified on this platform")
}
