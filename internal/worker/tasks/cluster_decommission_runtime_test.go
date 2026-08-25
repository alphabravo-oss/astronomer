package tasks

import (
	"strings"
	"testing"
)

type decommissionRuntimeCache struct{}

func (decommissionRuntimeCache) InvalidateAll() {}

func TestClusterDecommissionRuntimeValidatesAndBindsFamily(t *testing.T) {
	err := (ClusterDecommissionRuntime{}).Validate()
	if err == nil {
		t.Fatal("empty cluster-decommission runtime validated successfully")
	}
	for _, dependency := range []string{"queries", "tunnel", "rbac_cache"} {
		if !strings.Contains(err.Error(), dependency) {
			t.Errorf("validation error %q does not report %s", err, dependency)
		}
	}
	runtime := ClusterDecommissionRuntime{Deps: ClusterDecommissionDeps{
		Queries: newFakeDecommQuerier(), Tunnel: &fakeTunnel{}, RBACCache: decommissionRuntimeCache{},
	}}
	bindings, err := runtime.HandlerBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 || bindings[ClusterDecommissionType] == nil || bindings[ClusterDecommissionAllType] == nil {
		t.Fatalf("cluster-decommission bindings = %#v", bindings)
	}
	var typedNil *fakeDecommQuerier
	runtime.Deps.Queries = typedNil
	if err := runtime.Validate(); err == nil || !strings.Contains(err.Error(), "queries") {
		t.Fatalf("typed-nil validation error = %v", err)
	}
}
