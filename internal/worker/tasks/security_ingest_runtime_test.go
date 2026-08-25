package tasks

import (
	"strings"
	"testing"
)

func completeSecurityIngestRuntime() SecurityIngestRuntime {
	return SecurityIngestRuntime{
		Deps: SecurityIngestDeps{
			Queries: &securityLifecycleFake{},
			K8s:     &securityFetchRequester{},
			Outbox:  &securityOutboxFake{},
			Owner:   "test-runtime",
		},
		Leader: &fakeLeader{held: true},
	}
}

func TestSecurityIngestRuntimeValidationReportsMissingDependencies(t *testing.T) {
	err := (SecurityIngestRuntime{}).Validate()
	if err == nil {
		t.Fatal("empty runtime passed validation")
	}
	for _, dependency := range []string{"queries", "k8s_fetcher", "task_outbox", "leader"} {
		if !strings.Contains(err.Error(), dependency) {
			t.Errorf("validation error %q does not report %q", err, dependency)
		}
	}
}

func TestSecurityIngestRuntimeRejectsTypedNilQueries(t *testing.T) {
	runtime := completeSecurityIngestRuntime()
	var queries *securityLifecycleFake
	runtime.Deps.Queries = queries
	if err := runtime.Validate(); err == nil || !strings.Contains(err.Error(), "queries") {
		t.Fatalf("typed-nil validation error = %v", err)
	}
}

func TestSecurityIngestRuntimeBindsHandlers(t *testing.T) {
	bindings, err := completeSecurityIngestRuntime().HandlerBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 || bindings[SecurityIngestType] == nil || bindings[SecurityIngestRecoveryType] == nil {
		t.Fatalf("unexpected bindings: %#v", bindings)
	}
}
