package databasejob

import (
	"bytes"
	"errors"
	"strings"
	"time"
)

// OrderedMeasurements remains DATA. The worker must obtain logs/status from the
// actual owned Kubernetes objects and commit only under the locked current
// accepted feed, assignment, workload and measured runtime image identities.
type OrderedMeasurements struct {
	Initial, Final                         Measurement
	InitialLogTimestamp, FinalLogTimestamp time.Time
}

// DecodeOrderedMeasurements refuses partial/periodic/replayed sidecar logs.
// Kubernetes timestamps must prove initial reads ended before scanner start and
// final reads ended after scanner termination. Helper-clock timestamps are an
// additional consistency check, never the sole ordering authority.
func DecodeOrderedMeasurements(logs []byte, expected Preparation, scannerStarted, scannerFinished, helperFinished time.Time) (OrderedMeasurements, error) {
	if len(logs) == 0 || len(logs) > 2*(MaximumRecordBytes+128) || expected.validate() != nil || !utcClock(scannerStarted) || !utcClock(scannerFinished) || !utcClock(helperFinished) || scannerFinished.Before(scannerStarted) || helperFinished.Before(scannerFinished) {
		return OrderedMeasurements{}, errors.New("database scan termination/measurement inputs invalid")
	}
	lines := bytes.Split(bytes.TrimSuffix(logs, []byte{'\n'}), []byte{'\n'})
	if len(lines) != 2 {
		return OrderedMeasurements{}, errors.New("database scan needs exactly initial and final measurements")
	}
	var result OrderedMeasurements
	for index, line := range lines {
		stamp, payload, found := strings.Cut(string(line), " ")
		clock, err := time.Parse(time.RFC3339Nano, stamp)
		if !found || err != nil || !utcClock(clock) {
			return OrderedMeasurements{}, errors.New("database scan Kubernetes log timestamp invalid")
		}
		row, err := DecodeMeasurement([]byte(payload), expected)
		if err != nil {
			return OrderedMeasurements{}, err
		}
		difference := clock.Sub(row.ObservedAt)
		if difference < -5*time.Minute || difference > 5*time.Minute {
			return OrderedMeasurements{}, errors.New("database scan helper clock differs from Kubernetes")
		}
		if index == 0 {
			if row.Phase != "initial" || clock.After(scannerStarted) || row.ObservedAt.After(scannerStarted) {
				return OrderedMeasurements{}, errors.New("database scan initial measurement follows scanner start")
			}
			result.Initial, result.InitialLogTimestamp = row, clock
		} else {
			if row.Phase != "final" || clock.Before(scannerFinished) || row.ObservedAt.Before(scannerFinished) || clock.After(helperFinished) || row.ObservedAt.After(helperFinished) || !clock.After(result.InitialLogTimestamp) || !row.ObservedAt.After(result.Initial.ObservedAt) {
				return OrderedMeasurements{}, errors.New("database scan final measurement lacks post-termination ordering")
			}
			result.Final, result.FinalLogTimestamp = row, clock
		}
	}
	return result, nil
}
func utcClock(value time.Time) bool { return !value.IsZero() && value.Location() == time.UTC }
