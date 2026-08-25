package tasks

import (
	"context"
	"strings"
	"testing"

	"github.com/hibiken/asynq"
)

type coreRuntimeQueries struct{ RuntimeQuerier }
type coreRuntimeK8s struct{ K8sRequester }
type coreRuntimeDecryptor struct{}

func (*coreRuntimeDecryptor) DecryptBytes(value string) ([]byte, error) { return []byte(value), nil }

func TestCoreRuntimeBindHandlerScopesDependenciesPerInvocation(t *testing.T) {
	values := make(chan string, 2)
	handler := func(ctx context.Context, _ *asynq.Task) error {
		values <- runtimeDependencies(ctx).PlatformName
		return nil
	}

	alpha := (CoreRuntime{Deps: RuntimeDependencies{PlatformName: "alpha"}}).BindHandler(handler)
	beta := (CoreRuntime{Deps: RuntimeDependencies{PlatformName: "beta"}}).BindHandler(handler)
	if err := alpha(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := beta(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	if first, second := <-values, <-values; first != "alpha" || second != "beta" {
		t.Fatalf("runtime values crossed invocation boundaries: %q, %q", first, second)
	}
}

func TestCoreRuntimeContextNormalizesWithoutMutatingCaller(t *testing.T) {
	deps := RuntimeDependencies{}
	ctx := (CoreRuntime{Deps: deps}).Context(context.Background())
	normalized := runtimeDependencies(ctx)
	if normalized.HTTPClient == nil || normalized.Log == nil || normalized.RegistrationTokenTTLHours != 1 {
		t.Fatalf("runtime was not normalized: %+v", normalized)
	}
	if deps.HTTPClient != nil || deps.Log != nil || deps.RegistrationTokenTTLHours != 0 {
		t.Fatalf("caller-owned dependencies were mutated: %+v", deps)
	}
}

func TestCoreRuntimeValidationRejectsTypedNilQueries(t *testing.T) {
	var queries *coreRuntimeQueries
	runtime := CoreRuntime{Deps: RuntimeDependencies{
		Queries: queries,
		Leader:  &fakeLeader{held: true},
		K8s:     &coreRuntimeK8s{},
	}}
	if err := runtime.ValidateTunnel(); err == nil || !strings.Contains(err.Error(), "queries") {
		t.Fatalf("typed-nil validation error = %v", err)
	}
}

func TestTunnelCoreDoesNotRequireManagementBackup(t *testing.T) {
	runtime := CoreRuntime{Deps: RuntimeDependencies{
		Queries:           &coreRuntimeQueries{},
		Leader:            &fakeLeader{held: true},
		K8s:               &coreRuntimeK8s{},
		ResourceDecryptor: &coreRuntimeDecryptor{},
		ManagementBackup:  notReadyManagementBackup{},
	}}
	if err := runtime.ValidateTunnel(); err != nil {
		t.Fatalf("tunnel core unexpectedly requires management backup: %v", err)
	}
}
