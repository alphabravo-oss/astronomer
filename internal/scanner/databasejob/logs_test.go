package databasejob

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestDatabaseJobLogOrderingNeedsActualPostScannerMeasurement(t *testing.T) {
	value := preparationData()
	started := time.Date(2026, 10, 10, 2, 0, 0, 0, time.UTC)
	finished := started.Add(time.Minute)
	helperFinished := finished.Add(time.Minute)
	line := func(phase string, observed, stamp time.Time) []byte {
		t.Helper()
		var out bytes.Buffer
		if err := emitMeasurement(&out, value, phase, value.Cache, observed); err != nil {
			t.Fatal(err)
		}
		return append([]byte(stamp.Format(time.RFC3339Nano)+" "), out.Bytes()...)
	}
	initial := line("initial", started.Add(-time.Second), started.Add(-time.Second))
	final := line("final", finished.Add(time.Second), finished.Add(time.Second))
	valid := append(append([]byte(nil), initial...), final...)
	if _, err := DecodeOrderedMeasurements(valid, value, started, finished, helperFinished); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"missing-final":          initial,
		"missing-initial":        final,
		"replayed-initial":       append(append([]byte(nil), initial...), initial...),
		"third-line":             append(append([]byte(nil), valid...), final...),
		"final-before-exit":      append(append([]byte(nil), initial...), line("final", finished.Add(-time.Second), finished.Add(-time.Second))...),
		"kubernetes-before-exit": append(append([]byte(nil), initial...), line("final", finished.Add(time.Second), finished.Add(-time.Second))...),
		"initial-after-start":    append(line("initial", started.Add(time.Second), started.Add(time.Second)), final...),
		"final-after-helper":     append(append([]byte(nil), initial...), line("final", helperFinished.Add(time.Second), helperFinished.Add(time.Second))...),
		"clock-skew":             append(append([]byte(nil), initial...), line("final", finished.Add(time.Second), finished.Add(10*time.Minute))...),
		"unprefixed":             bytes.Replace(valid, []byte(started.Add(-time.Second).Format(time.RFC3339Nano)+" "), nil, 1),
		"oversized":              []byte(strings.Repeat("x", 2*(MaximumRecordBytes+128)+1)),
	}
	for name, logs := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeOrderedMeasurements(logs, value, started, finished, helperFinished); err == nil {
				t.Fatal("incomplete/early measurement accepted")
			}
		})
	}
	for name, change := range map[string]func(*Preparation){"feed": func(v *Preparation) { v.Cache.FeedDigest = "sha256:" + strings.Repeat("c", 64) }, "scanner": func(v *Preparation) { v.ScannerDigest = "sha256:" + strings.Repeat("c", 64) }} {
		t.Run(name, func(t *testing.T) {
			wrong := value
			change(&wrong)
			if _, err := DecodeOrderedMeasurements(valid, wrong, started, finished, helperFinished); err == nil {
				t.Fatal("foreign current comparison accepted")
			}
		})
	}
}
