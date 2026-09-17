package main

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestMandatoryAuditScheduleStartsImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var sequences []int
	runMandatoryAuditOperations(ctx, 1, nil, func(sequence int) {
		sequences = append(sequences, sequence)
	})

	if want := []int{0}; !reflect.DeepEqual(sequences, want) {
		t.Fatalf("operation sequences = %v, want %v", sequences, want)
	}
}

func TestMandatoryAuditScheduleDoesNotStartAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()

	var sequences []int
	runMandatoryAuditOperations(ctx, 2, ticks, func(sequence int) {
		sequences = append(sequences, sequence)
		cancel()
	})

	if want := []int{0}; !reflect.DeepEqual(sequences, want) {
		t.Fatalf("operation sequences = %v, want %v", sequences, want)
	}
}

func TestMandatoryAuditTargetEndsBeforeDeadlineTick(t *testing.T) {
	// A 10-second, 1/s window needs operations at t=0..9s: ten operations,
	// never an eleventh at t=10s where cancellation and the tick race.
	ticks := make(chan time.Time, 10)
	for range 10 {
		ticks <- time.Now()
	}
	var sequences []int
	runMandatoryAuditOperations(context.Background(), mandatoryAuditTargetOperations(10*time.Second, 1), ticks, func(sequence int) {
		sequences = append(sequences, sequence)
	})
	if len(sequences) != 10 || sequences[0] != 0 || sequences[len(sequences)-1] != 9 {
		t.Fatalf("operation sequences = %v, want 0..9", sequences)
	}
}
