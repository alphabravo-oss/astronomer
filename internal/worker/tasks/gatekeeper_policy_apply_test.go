package tasks

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/gatekeeperpolicy"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type gatekeeperPolicyK8s struct {
	supported       bool
	capabilityErr   error
	capabilityCalls int
	doCalls         int
}

func (k *gatekeeperPolicyK8s) SupportsCapability(_ context.Context, _, capability string) (bool, error) {
	k.capabilityCalls++
	if capability != protocol.AgentCapabilityMutate {
		return false, errors.New("unexpected capability")
	}
	return k.supported, k.capabilityErr
}

func (k *gatekeeperPolicyK8s) Do(_ context.Context, _, _, _ string, _ []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	k.doCalls++
	return &protocol.K8sResponsePayload{StatusCode: http.StatusOK}, nil
}

func gatekeeperPolicyManifestFixture() []gatekeeperpolicy.Manifest {
	return []gatekeeperpolicy.Manifest{{
		Group: "templates.gatekeeper.sh", Version: "v1", Resource: "constrainttemplates",
		Kind: "ConstraintTemplate", Name: "required-labels", JSON: []byte(`{"kind":"ConstraintTemplate"}`),
	}}
}

func TestGatekeeperPolicySkipsReadOnlyAgentBeforeAnyK8sRequest(t *testing.T) {
	k8s := &gatekeeperPolicyK8s{supported: false}
	ctx := testRuntimeContext(RuntimeDependencies{K8s: k8s})

	reconcileGatekeeperPolicyCluster(ctx, uuid.New(), gatekeeperPolicyManifestFixture(), k8s)

	if k8s.capabilityCalls != 1 {
		t.Fatalf("capability calls = %d, want 1", k8s.capabilityCalls)
	}
	if k8s.doCalls != 0 {
		t.Fatalf("read-only agent received %d Kubernetes requests, want 0", k8s.doCalls)
	}
}

func TestGatekeeperPolicySkipsUnknownCapabilityStateFailClosed(t *testing.T) {
	k8s := &gatekeeperPolicyK8s{capabilityErr: errors.New("agent disconnected")}
	ctx := testRuntimeContext(RuntimeDependencies{K8s: k8s})

	reconcileGatekeeperPolicyCluster(ctx, uuid.New(), gatekeeperPolicyManifestFixture(), k8s)

	if k8s.doCalls != 0 {
		t.Fatalf("unknown capability state received %d Kubernetes requests, want 0", k8s.doCalls)
	}
}

func TestGatekeeperPolicyProbesAndAppliesForMutationCapableAgent(t *testing.T) {
	k8s := &gatekeeperPolicyK8s{supported: true}
	ctx := testRuntimeContext(RuntimeDependencies{K8s: k8s})

	reconcileGatekeeperPolicyCluster(ctx, uuid.New(), gatekeeperPolicyManifestFixture(), k8s)

	if k8s.doCalls != 2 {
		t.Fatalf("Kubernetes requests = %d, want discovery GET plus apply PATCH", k8s.doCalls)
	}
}
