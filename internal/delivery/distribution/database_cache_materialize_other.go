//go:build !unix

package distribution

import (
	"context"
	"errors"
	"io"
)

func createDatabaseCacheDestination(string, int64) (*MaterializedDatabaseCache, error) {
	return nil, errors.New("owned scan cache materialization is not qualified on this platform")
}
func (*MaterializedDatabaseCache) createMember(int, string, int64, DatabaseUpdateSnapshot, DatabaseCacheContent) (io.Writer, error) {
	return nil, errors.New("owned scan cache materialization unavailable")
}
func (*MaterializedDatabaseCache) sealAndMeasure(context.Context) error {
	return errors.New("owned scan cache materialization unavailable")
}

func (*MaterializedDatabaseCache) Measure(context.Context) (DatabaseCacheSnapshot, error) {
	return DatabaseCacheSnapshot{}, errors.New("owned scan cache measurement unavailable")
}

func createDatabaseCacheDestinationForJob(string, int64, string) (*MaterializedDatabaseCache, error) {
	return nil, errors.New("owned scan cache materialization is not qualified on this platform")
}
