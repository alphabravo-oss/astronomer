//go:build !linux

package databasejob

import (
	"context"
	"errors"
	"io"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
)

func createWorkloadInput(context.Context, string, uuid.UUID, resolver.WorkloadImageMetadata, string, string) (*WorkloadInput, error) {
	return nil, errors.New("owned workload input requires qualified Linux filesystem semantics")
}
func reopenWorkloadInput(context.Context, string, uuid.UUID, WorkloadInputSnapshot) (*WorkloadInput, error) {
	return nil, errors.New("owned workload input requires qualified Linux filesystem semantics")
}
func (*WorkloadInput) createBlob(WorkloadInputMember) (io.Writer, bool, error) {
	return nil, false, errors.New("owned workload input platform unavailable")
}
func (*WorkloadInput) sealAndMeasure(context.Context) error {
	return errors.New("owned workload input platform unavailable")
}
func (*WorkloadInput) measure(context.Context) error {
	return errors.New("owned workload input platform unavailable")
}
func (*WorkloadInput) measureExisting(context.Context, WorkloadInputMember) error {
	return errors.New("owned workload input platform unavailable")
}
