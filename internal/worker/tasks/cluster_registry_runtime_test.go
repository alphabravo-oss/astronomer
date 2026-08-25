package tasks

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

type clusterRegistryRuntimeQueries struct{ ClusterRegistryApplyQuerier }

func TestClusterRegistryRuntimeValidatesAndBindsFamily(t *testing.T) {
	err := (ClusterRegistryRuntime{}).Validate()
	if err == nil {
		t.Fatal("empty cluster-registry runtime validated successfully")
	}
	for _, dependency := range []string{"queries", "requester", "encryptor"} {
		if !strings.Contains(err.Error(), dependency) {
			t.Errorf("validation error %q does not report %s", err, dependency)
		}
	}
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	runtime := ClusterRegistryRuntime{Deps: ClusterRegistryApplyDeps{
		Queries: clusterRegistryRuntimeQueries{}, Requester: &fakeProjectK8sRequester{}, Encryptor: encryptor,
	}}
	bindings, err := runtime.HandlerBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 || bindings[ClusterApplyRegistrySecretType] == nil || bindings[ClusterRegistryDriftReconcileType] == nil {
		t.Fatalf("cluster-registry bindings = %#v", bindings)
	}
	var typedNil *clusterRegistryRuntimeQueries
	runtime.Deps.Queries = typedNil
	if err := runtime.Validate(); err == nil || !strings.Contains(err.Error(), "queries") {
		t.Fatalf("typed-nil validation error = %v", err)
	}
}
