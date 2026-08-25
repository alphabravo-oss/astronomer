package tasks

import (
	"strings"
	"testing"
)

func completeGitOpsRuntime() GitOpsRuntime {
	q := newFakeQuerier()
	return GitOpsRuntime{Deps: GitOpsDeps{
		Queries: q, TaskOutbox: &fakeTaskOutboxWriter{}, Decryptor: fakeDecryptor{},
	}}
}

func TestGitOpsRuntimeRejectsMissingAndTypedNilDependencies(t *testing.T) {
	err := (GitOpsRuntime{}).Validate()
	if err == nil {
		t.Fatal("empty GitOps runtime validated successfully")
	}
	for _, dependency := range []string{"queries", "task_outbox", "decryptor"} {
		if !strings.Contains(err.Error(), dependency) {
			t.Errorf("validation error %q does not report %s", err, dependency)
		}
	}

	runtime := completeGitOpsRuntime()
	var typedNil *fakeGitOpsQuerier
	runtime.Deps.Queries = typedNil
	if err := runtime.Validate(); err == nil || !strings.Contains(err.Error(), "queries") {
		t.Fatalf("typed-nil query validation error = %v", err)
	}
}

func TestGitOpsRuntimeBindsSyncHandler(t *testing.T) {
	bindings, err := completeGitOpsRuntime().HandlerBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[GitOpsSyncType] == nil {
		t.Fatalf("GitOps bindings = %#v", bindings)
	}
}
