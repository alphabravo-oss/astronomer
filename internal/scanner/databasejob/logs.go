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
// final reads ended after the entire scanner termination precision interval.
// Kubernetes metav1.Time JSON endpoints represent seconds, not precise instants.
// Helper-clock timestamps are an
// additional consistency check, never the sole ordering authority.
func DecodeOrderedMeasurements(logs []byte, expected Preparation, scannerStarted, scannerFinished, helperFinished time.Time) (OrderedMeasurements, error) {
	if len(logs) == 0 || len(logs) > 2*(MaximumRecordBytes+128) || expected.validate() != nil || !utcClock(scannerStarted) || !utcClock(scannerFinished) || !utcClock(helperFinished) || scannerFinished.Before(scannerStarted) || helperFinished.Before(scannerFinished) {
		return OrderedMeasurements{}, errors.New("database scan termination/measurement inputs invalid")
	}
	startedFloor, _ := KubernetesTerminationInterval(scannerStarted)
	_, finishedUpper := KubernetesTerminationInterval(scannerFinished)
	_, helperUpper := KubernetesTerminationInterval(helperFinished)
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
			if row.Phase != "initial" || clock.After(startedFloor) || row.ObservedAt.After(startedFloor) {
				return OrderedMeasurements{}, errors.New("database scan initial measurement follows scanner start")
			}
			result.Initial, result.InitialLogTimestamp = row, clock
		} else {
			if row.Phase != "final" || clock.Before(finishedUpper) || row.ObservedAt.Before(finishedUpper) || !clock.Before(helperUpper) || !row.ObservedAt.Before(helperUpper) || !clock.After(result.InitialLogTimestamp) || !row.ObservedAt.After(result.Initial.ObservedAt) {
				return OrderedMeasurements{}, errors.New("database scan final measurement lacks post-termination ordering")
			}
			result.Final, result.FinalLogTimestamp = row, clock
		}
	}
	return result, nil
}
func utcClock(value time.Time) bool { return !value.IsZero() && value.Location() == time.UTC }

// KubernetesTerminationInterval returns DATA bounds for a decoded metav1.Time
// termination endpoint. The upper bound is exclusive. Consumers must validate
// nonzero UTC endpoints before calling; these bounds do not establish duration.
func KubernetesTerminationInterval(value time.Time) (time.Time, time.Time) {
	lower := value.Truncate(time.Second)
	return lower, lower.Add(time.Second)
}
