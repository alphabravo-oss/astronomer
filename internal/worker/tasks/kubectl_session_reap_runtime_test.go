package tasks

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/kubectl"
)

func completeKubectlSessionReapRuntime() KubectlSessionReapRuntime {
	return KubectlSessionReapRuntime{
		Deps:   kubectl.Deps{Queries: newFakeReapQuerier(), Requester: &fakeReapRequester{}},
		Leader: &fakeLeader{held: true},
	}
}

func TestKubectlSessionReapRuntimeValidationReportsMissingDependencies(t *testing.T) {
	err := (KubectlSessionReapRuntime{}).Validate()
	if err == nil {
		t.Fatal("empty runtime passed validation")
	}
	for _, dependency := range []string{"queries", "requester", "leader"} {
		if !strings.Contains(err.Error(), dependency) {
			t.Errorf("validation error %q does not report %q", err, dependency)
		}
	}
}

func TestKubectlSessionReapRuntimeRejectsTypedNilQueries(t *testing.T) {
	runtime := completeKubectlSessionReapRuntime()
	var queries *fakeReapQuerier
	runtime.Deps.Queries = queries
	if err := runtime.Validate(); err == nil || !strings.Contains(err.Error(), "queries") {
		t.Fatalf("typed-nil validation error = %v", err)
	}
}

func TestKubectlSessionReapRuntimeBindsOptionalHandler(t *testing.T) {
	bindings, err := (KubectlSessionReapRuntime{}).HandlerBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[KubectlSessionReapType] == nil {
		t.Fatalf("unexpected bindings: %#v", bindings)
	}
}
