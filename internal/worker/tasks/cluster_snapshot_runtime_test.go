package tasks

import (
	"strings"
	"testing"
)

func TestClusterSnapshotRuntimeValidatesAndBindsFamily(t *testing.T) {
	err := (ClusterSnapshotRuntime{}).Validate()
	if err == nil || !strings.Contains(err.Error(), "queries") || !strings.Contains(err.Error(), "driver") {
		t.Fatalf("empty runtime validation error = %v", err)
	}
	runtime := ClusterSnapshotRuntime{Deps: ClusterSnapshotDeps{
		Queries: newFakePollQuerier(), Driver: newFakeDriver(),
	}}
	bindings, err := runtime.HandlerBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 4 || bindings[ClusterSnapshotApplyOperationType] == nil || bindings[ClusterSnapshotPollType] == nil || bindings[ClusterSnapshotDispatchScheduledType] == nil || bindings[ClusterSnapshotCleanupExpiredType] == nil {
		t.Fatalf("cluster-snapshot bindings = %#v", bindings)
	}
	var typedNil *fakePollQuerier
	runtime.Deps.Queries = typedNil
	if err := runtime.Validate(); err == nil || !strings.Contains(err.Error(), "queries") {
		t.Fatalf("typed-nil validation error = %v", err)
	}
}
