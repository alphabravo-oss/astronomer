package tasks

import (
	"strings"
	"testing"
)

func TestCloudCredentialRuntimeValidatesAndBindsFamily(t *testing.T) {
	err := (CloudCredentialRuntime{}).Validate()
	if err == nil {
		t.Fatal("empty cloud-credential runtime validated successfully")
	}
	for _, dependency := range []string{"queries", "requester", "decryptor"} {
		if !strings.Contains(err.Error(), dependency) {
			t.Errorf("validation error %q does not report %s", err, dependency)
		}
	}
	q := newFakeCloudCredentialQuerier()
	runtime := CloudCredentialRuntime{Deps: CloudCredentialMaterializeDeps{
		Queries: q, Requester: &fakeProjectK8sRequester{}, Decryptor: fakeDecryptor{},
	}}
	bindings, err := runtime.HandlerBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 || bindings[CloudCredentialMaterializeType] == nil || bindings[CloudCredentialDriftReconcileType] == nil {
		t.Fatalf("cloud-credential bindings = %#v", bindings)
	}
	var typedNil *fakeCloudCredentialQuerier
	runtime.Deps.Queries = typedNil
	if err := runtime.Validate(); err == nil || !strings.Contains(err.Error(), "queries") {
		t.Fatalf("typed-nil validation error = %v", err)
	}
}
