package tasks

import (
	"errors"
	"math"
)

// snapshotStatusCounts validates the observed Velero counters before a phase
// write, completion metric or change event. Existing row repair paths are separate.
func snapshotStatusCounts(warnings, errorsCount int) (int32, int32, error) {
	if warnings < 0 || warnings > math.MaxInt32 {
		return 0, 0, errors.New("snapshot warning count is outside its supported range")
	}
	if errorsCount < 0 || errorsCount > math.MaxInt32 {
		return 0, 0, errors.New("snapshot error count is outside its supported range")
	}
	return int32(warnings), int32(errorsCount), nil
}
