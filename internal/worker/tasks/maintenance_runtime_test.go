package tasks

import (
	"context"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func completeMaintenanceRuntime(t *testing.T) MaintenanceRuntime {
	t.Helper()
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	return MaintenanceRuntime{
		AgentTokens: AgentTokenRotateDeps{Queries: &fakeAgentTokenRotateQuerier{}},
		PlaintextCredentials: PlaintextCredentialMigrationDeps{
			Queries: &fakePlaintextCredentialMigrationQuerier{}, Encryptor: encryptor,
		},
	}
}

func TestMaintenanceRuntimeRejectsMissingAndTypedNilDependencies(t *testing.T) {
	err := (MaintenanceRuntime{}).Validate()
	if err == nil {
		t.Fatal("empty maintenance runtime validated successfully")
	}
	for _, dependency := range []string{
		"agent_tokens.queries", "plaintext_credentials.queries",
		"plaintext_credentials.encryptor",
	} {
		if !strings.Contains(err.Error(), dependency) {
			t.Errorf("validation error %q does not report %s", err, dependency)
		}
	}

	runtime := completeMaintenanceRuntime(t)
	var typedNil *fakeAgentTokenRotateQuerier
	runtime.AgentTokens.Queries = typedNil
	if err := runtime.Validate(); err == nil || !strings.Contains(err.Error(), "agent_tokens.queries") {
		t.Fatalf("typed-nil agent-token query validation error = %v", err)
	}
}

func TestMaintenanceRuntimeBindsCompleteHandlerFamily(t *testing.T) {
	bindings, err := completeMaintenanceRuntime(t).HandlerBindings()
	if err != nil {
		t.Fatal(err)
	}
	for _, taskType := range []string{
		AgentTokenRotateSweepType, PlaintextCredentialMigrationType,
	} {
		if bindings[taskType] == nil {
			t.Errorf("handler %q is not bound", taskType)
		}
	}
	if len(bindings) != 2 {
		t.Fatalf("handler bindings = %d, want 2", len(bindings))
	}
}

func TestCRDOwnershipHandlerFailsClosedWithoutDynamicClient(t *testing.T) {
	runtime := CRDOwnershipRuntime{Deps: CRDOwnershipDriftDeps{Queries: &fakeCRDOwnershipDriftQuerier{}}}
	if err := runtime.HandleCRDOwnershipDriftCheck(context.Background(), nil); err == nil {
		t.Fatal("CRD ownership handler acknowledged a missing dynamic client")
	}
}

func TestCRDOwnershipRuntimeBindsOnlyTunnelHandler(t *testing.T) {
	runtime := CRDOwnershipRuntime{Deps: CRDOwnershipDriftDeps{
		Queries: &fakeCRDOwnershipDriftQuerier{},
		Dynamic: dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme()),
	}}
	bindings, err := runtime.HandlerBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[CRDOwnershipDriftCheckType] == nil {
		t.Fatalf("CRD ownership bindings = %#v", bindings)
	}
}
