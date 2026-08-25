package tasks

import (
	"context"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func completeDeferredRuntime() DeferredRuntime {
	replayers := make(map[string]DeferredReplayer, len(requiredDeferredReplayTypes))
	for _, operationType := range requiredDeferredReplayTypes {
		replayers[operationType] = func(context.Context, sqlc.DeferredOperation) error { return nil }
	}
	return DeferredRuntime{Deps: DeferredDispatchDeps{
		Queries:   &fakeDeferredQuerier{},
		Replayers: replayers,
	}}
}

func TestDeferredRuntimeValidationReportsEveryMissingDependency(t *testing.T) {
	err := (DeferredRuntime{}).Validate()
	if err == nil {
		t.Fatal("empty runtime passed validation")
	}
	for _, dependency := range append([]string{"queries"}, requiredDeferredReplayTypes...) {
		if !strings.Contains(err.Error(), dependency) {
			t.Errorf("validation error %q does not report %q", err, dependency)
		}
	}
}

func TestDeferredRuntimeValidationRejectsTypedNilQueries(t *testing.T) {
	runtime := completeDeferredRuntime()
	var queries *fakeDeferredQuerier
	runtime.Deps.Queries = queries
	if err := runtime.Validate(); err == nil || !strings.Contains(err.Error(), "queries") {
		t.Fatalf("typed-nil queries validation error = %v", err)
	}
}

func TestDeferredRuntimeBindsDispatchHandler(t *testing.T) {
	bindings, err := completeDeferredRuntime().HandlerBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[DispatchDeferredType] == nil {
		t.Fatalf("unexpected bindings: %#v", bindings)
	}
}
