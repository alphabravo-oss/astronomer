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
