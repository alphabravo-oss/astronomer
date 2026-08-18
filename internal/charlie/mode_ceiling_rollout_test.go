package charlie

import (
	"context"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type ceilingQueriesFake struct{ connection sqlc.CharlieConnection }

func (f ceilingQueriesFake) GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	return f.connection, nil
}

type ceilingHelmFake struct {
	specs []HelmReleaseSpec
	err   error
}

func (f *ceilingHelmFake) Apply(_ context.Context, spec HelmReleaseSpec) error {
	f.specs = append(f.specs, spec)
	return f.err
}
func (f *ceilingHelmFake) Uninstall(context.Context) error { return nil }

type ceilingWorkloadFake struct {
	calls []AgentWorkloadStatus
}

func (f *ceilingWorkloadFake) AgentWorkload(context.Context) (AgentWorkloadStatus, error) {
	if len(f.calls) == 0 {
		return AgentWorkloadStatus{}, nil
	}
	next := f.calls[0]
	if len(f.calls) > 1 {
		f.calls = f.calls[1:]
	}
	return next, nil
}

type ceilingBridgeFake struct {
	mode   Mode
	status AdminBridgeStatus
	err    error
}

func (f *ceilingBridgeFake) SetAdminMode(_ context.Context, mode Mode) (AdminBridgeStatus, error) {
	f.mode = mode
	if f.err != nil {
		return AdminBridgeStatus{}, f.err
	}
	return f.status, nil
}

func TestHelmModeCeilingRolloutDoesNotLowerCeilingBelowRequested(t *testing.T) {
	helm := &ceilingHelmFake{}
	bridge := &ceilingBridgeFake{status: AdminBridgeStatus{ProductModeCeiling: "read_only", ProductEnabled: true}}
	rollout := &HelmModeCeilingRollout{
		Queries: ceilingQueriesFake{connection: sqlc.CharlieConnection{Active: true, ChartReference: "oci://example/chart"}},
		Helm:    helm,
		Workload: &ceilingWorkloadFake{calls: []AgentWorkloadStatus{
			{Present: true, Desired: 2, Ready: 2, ModeCeiling: ModeReadOnly},
		}},
		Bridge: bridge,
	}
	if err := rollout.Reconcile(context.Background(), ModeCeilingTarget{
		Desired: ModeDisabled, ExpectedRequested: ModeReadOnly, ConnectionID: "c",
	}); err != nil {
		t.Fatal(err)
	}
	if len(helm.specs) != 0 || bridge.mode != ModeReadOnly {
		t.Fatalf("background reconcile lowered the product ceiling: helm=%d bridge=%s", len(helm.specs), bridge.mode)
	}
}

func TestHelmModeCeilingRolloutSkipsHelmWhenReplicasAlreadyMatch(t *testing.T) {
	helm := &ceilingHelmFake{}
	bridge := &ceilingBridgeFake{status: AdminBridgeStatus{ProductModeCeiling: "read_only", ProductEnabled: true}}
	rollout := &HelmModeCeilingRollout{
		Queries:  ceilingQueriesFake{connection: sqlc.CharlieConnection{Active: true, ChartReference: "oci://example/chart"}},
		Helm:     helm,
		Workload: &ceilingWorkloadFake{calls: []AgentWorkloadStatus{{Present: true, Desired: 2, Ready: 2, ModeCeiling: ModeReadOnly}}},
		Bridge:   bridge,
	}
	if err := rollout.Reconcile(context.Background(), ModeCeilingTarget{Desired: ModeReadOnly, ConnectionID: "c"}); err != nil {
		t.Fatal(err)
	}
	if len(helm.specs) != 0 || bridge.mode != ModeReadOnly {
		t.Fatalf("unexpected helm=%d bridge=%s", len(helm.specs), bridge.mode)
	}
}

func TestHelmModeCeilingRolloutUpgradesThenWaitsForBothReplicas(t *testing.T) {
	helm := &ceilingHelmFake{}
	bridge := &ceilingBridgeFake{status: AdminBridgeStatus{ProductModeCeiling: "read_only", ProductEnabled: true}}
	rollout := &HelmModeCeilingRollout{
		Queries: ceilingQueriesFake{connection: sqlc.CharlieConnection{
			Active: true, ChartReference: "oci://example/chart", ChartDigest: "sha256:abc",
		}},
		Helm: helm,
		Workload: &ceilingWorkloadFake{calls: []AgentWorkloadStatus{
			{Present: true, Desired: 2, Ready: 2, ModeCeiling: ModeDisabled},
			{Present: true, Desired: 2, Ready: 1, ModeCeiling: ModeReadOnly},
			{Present: true, Desired: 2, Ready: 2, ModeCeiling: ModeReadOnly},
		}},
		Bridge:  bridge,
		Timeout: time.Minute,
		Sleep:   func(time.Duration) {},
	}
	if err := rollout.Reconcile(context.Background(), ModeCeilingTarget{Desired: ModeReadOnly, ConnectionID: "c"}); err != nil {
		t.Fatal(err)
	}
	if len(helm.specs) != 1 || helm.specs[0].ReuseValues != true {
		t.Fatalf("helm apply missing reuse-values: %#v", helm.specs)
	}
	runtime, _ := helm.specs[0].Values["runtime"].(map[string]any)
	if runtime["modeCeiling"] != "read_only" || bridge.mode != ModeReadOnly {
		t.Fatalf("ceiling was not applied: values=%#v bridge=%s", helm.specs[0].Values, bridge.mode)
	}
}
