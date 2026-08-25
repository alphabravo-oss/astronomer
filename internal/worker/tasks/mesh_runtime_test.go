package tasks

import (
	"strings"
	"testing"
)

func TestMeshRuntimeValidatesAndBindsHandler(t *testing.T) {
	err := (MeshRuntime{}).Validate()
	if err == nil || !strings.Contains(err.Error(), "queries") || !strings.Contains(err.Error(), "requester") {
		t.Fatalf("empty runtime validation error = %v", err)
	}
	q := newFakeMeshQuerier()
	runtime := MeshRuntime{Deps: MeshDetectDeps{Queries: q, Requester: newScriptedRequester()}}
	bindings, err := runtime.HandlerBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[MeshDetectType] == nil {
		t.Fatalf("mesh bindings = %#v", bindings)
	}
	var typedNil *fakeMeshQuerier
	runtime.Deps.Queries = typedNil
	if err := runtime.Validate(); err == nil || !strings.Contains(err.Error(), "queries") {
		t.Fatalf("typed-nil validation error = %v", err)
	}
}
