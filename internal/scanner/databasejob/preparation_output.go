package databasejob

import (
	"context"
	"encoding/json"
	"errors"
	"io"
)

// WritePreparationRecord emits one bounded comparison record from actual Stage
// input preparation. The process adapter uses the SAME finite synchronous
// MeasurementPipe. Output receipt is not authentication or scan-use authority.
func WritePreparationRecord(ctx context.Context, writer io.Writer, value Preparation) error {
	if ctx == nil || writer == nil || value.validate() != nil || value.SchemaVersion != 2 || value.Workload == nil {
		return errors.New("owned database preparation output invalid")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := json.Marshal(value)
	if err != nil || len(payload) == 0 || len(payload) > MaximumRecordBytes {
		return errors.New("owned database preparation output exceeds bound")
	}
	payload = append(payload, '\n')
	n, err := writer.Write(payload)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || n != len(payload) {
		return errors.New("owned database preparation output incomplete")
	}
	return nil
}
