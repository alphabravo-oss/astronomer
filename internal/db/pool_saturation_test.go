package db

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestPoolSaturationStateIsPerDatabase(t *testing.T) {
	var first poolSaturationState
	var second poolSaturationState

	if !first.waitingForConnection(0, 1) {
		t.Fatal("first database did not report its new empty acquire")
	}
	if first.waitingForConnection(0, 1) {
		t.Fatal("unchanged counter remained saturated")
	}
	if !second.waitingForConnection(0, 1) {
		t.Fatal("first database state leaked into second database")
	}
	if first.waitingForConnection(1, 2) {
		t.Fatal("database with an idle connection reported saturation")
	}
}

func TestPoolSaturationStateConcurrentObservation(t *testing.T) {
	var state poolSaturationState
	var saturated atomic.Int64
	var group sync.WaitGroup
	for range 64 {
		group.Add(1)
		go func() {
			defer group.Done()
			if state.waitingForConnection(0, 1) {
				saturated.Add(1)
			}
		}()
	}
	group.Wait()
	if got := saturated.Load(); got != 1 {
		t.Fatalf("saturated observations = %d, want exactly one counter transition", got)
	}
}
