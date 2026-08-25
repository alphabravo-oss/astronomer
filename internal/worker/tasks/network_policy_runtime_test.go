package tasks

import (
	"strings"
	"testing"
)

func TestNetworkPolicyRuntimeValidatesAndBindsFamily(t *testing.T) {
	err := (NetworkPolicyRuntime{}).Validate()
	if err == nil || !strings.Contains(err.Error(), "queries") || !strings.Contains(err.Error(), "requester") {
		t.Fatalf("empty runtime validation error = %v", err)
	}
	runtime := NetworkPolicyRuntime{Deps: NetworkPolicyApplyDeps{
		Queries: newFakeNetPolQuerier(), Requester: &fakeK8sRequester{},
	}}
	bindings, err := runtime.HandlerBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 || bindings[NetworkPolicyApplyType] == nil || bindings[NetworkPolicyDriftCheckType] == nil {
		t.Fatalf("network-policy bindings = %#v", bindings)
	}
	var typedNil *fakeNetPolQuerier
	runtime.Deps.Queries = typedNil
	if err := runtime.Validate(); err == nil || !strings.Contains(err.Error(), "queries") {
		t.Fatalf("typed-nil validation error = %v", err)
	}
}
