//go:build !linux

package databasejob

import (
	"context"
	"errors"
	"os"
)

type MeasurementPipe struct{}

func NewMeasurementPipe(context.Context, *os.File) (*MeasurementPipe, error) {
	return nil, errors.New("database measurement pipe requires qualified Linux container role")
}
func (*MeasurementPipe) Write([]byte) (int, error) {
	return 0, errors.New("database measurement pipe requires qualified Linux container role")
}
func (*MeasurementPipe) Close() {}
