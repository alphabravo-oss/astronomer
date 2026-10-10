//go:build !linux

package databasejob

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

func InitializeJobInput(context.Context, uuid.UUID) error {
	return errors.New("owned scan initialization requires qualified Linux process")
}
func InitializeJobCoordination(context.Context) error {
	return errors.New("owned scan initialization requires qualified Linux process")
}
func InitializeJobMaterialDestination(context.Context) error {
	return errors.New("owned scan initialization requires qualified Linux process")
}
