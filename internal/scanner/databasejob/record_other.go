//go:build !linux

package databasejob

import (
	"context"
	"errors"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

// The optional measured helper is a Linux container role; ordinary CLI/server
// supported platforms are unchanged. No non-Linux filesystem claim is made.
func PublishPreparation(context.Context, string, Preparation) error {
	return errors.New("database scan preparation requires qualified Linux file ownership")
}
func readPreparation(context.Context, string) (Preparation, bool, error) {
	return Preparation{}, false, errors.New("database scan preparation requires qualified Linux file ownership")
}

func publishInitialMeasurement(context.Context, string, Preparation, distribution.DatabaseCacheSnapshot, time.Time) error {
	return errors.New("database scan initial acknowledgement requires qualified Linux file ownership")
}
func readOwnedRecord(context.Context, string, string) ([]byte, bool, error) {
	return nil, false, errors.New("database scan owned record requires qualified Linux file ownership")
}
