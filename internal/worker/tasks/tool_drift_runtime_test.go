package tasks

import (
	"strings"
	"testing"
)

func TestToolDriftRuntimeValidatesAndBindsHandler(t *testing.T) {
	err := (ToolDriftRuntime{}).Validate()
	if err == nil || !strings.Contains(err.Error(), "queries") || !strings.Contains(err.Error(), "helm") {
		t.Fatalf("empty runtime validation error = %v", err)
	}

	runtime := ToolDriftRuntime{Deps: ToolDriftSweepDeps{
		Queries: &fakeDriftQuerier{}, Helm: &fakeDriftHelm{},
	}}
	bindings, err := runtime.HandlerBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[ToolDriftSweepType] == nil {
		t.Fatalf("tool-drift bindings = %#v", bindings)
	}

	var typedNil *fakeDriftQuerier
	runtime.Deps.Queries = typedNil
	if err := runtime.Validate(); err == nil || !strings.Contains(err.Error(), "queries") {
		t.Fatalf("typed-nil query validation error = %v", err)
	}
}
