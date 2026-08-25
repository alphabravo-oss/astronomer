package tasks

import (
	"context"
	"strings"
	"testing"
)

func TestControlPlaneSnapshotRuntimeOptionalBindingAndValidation(t *testing.T) {
	bindings, err := (ControlPlaneSnapshotRuntime{}).HandlerBindings()
	if err != nil || len(bindings) != 2 || bindings[ControlPlaneSnapshotApplyType] == nil || bindings[ControlPlaneSnapshotSweepType] == nil {
		t.Fatalf("optional control-plane bindings=%#v error=%v", bindings, err)
	}
	if err := (ControlPlaneSnapshotRuntime{}).Validate(); err == nil {
		t.Fatal("empty enabled runtime validated successfully")
	}
	runtime := ControlPlaneSnapshotRuntime{
		Deps:         ControlPlaneSnapshotSweepDeps{Queries: &fakeCPSQuerier{}},
		Applier:      func(context.Context, string, string, string, string, string) error { return nil },
		StatusReader: func(context.Context, string, string) (string, string, error) { return "running", "", nil },
	}
	if err := runtime.Validate(); err != nil {
		t.Fatal(err)
	}
	var typedNil *fakeCPSQuerier
	runtime.Deps.Queries = typedNil
	if err := runtime.Validate(); err == nil || !strings.Contains(err.Error(), "queries") {
		t.Fatalf("typed-nil validation error = %v", err)
	}
}
