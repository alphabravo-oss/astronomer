package charlie

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type featureInstallerFake struct {
	resumeCalls  int
	suspendCalls int
	order        *[]string
}

func (f *featureInstallerFake) Suspend(context.Context, AgentInstallSpec) error {
	f.suspendCalls++
	if f.order != nil {
		*f.order = append(*f.order, "suspend")
	}
	return nil
}
func (f *featureInstallerFake) Resume(context.Context, AgentInstallSpec) error {
	f.resumeCalls++
	return nil
}

type featureRuntimeFake struct {
	activations int
	shutdowns   int
	order       *[]string
}

func (f *featureRuntimeFake) Activate(context.Context) error {
	f.activations++
	return nil
}
func (f *featureRuntimeFake) Shutdown(context.Context) error {
	f.shutdowns++
	if f.order != nil {
		*f.order = append(*f.order, "runtime")
	}
	return nil
}

type featureModeFake struct {
	order *[]string
	err   error
}

func (f featureModeFake) EmergencyDisable(context.Context, string) (ModeState, error) {
	if f.order != nil {
		*f.order = append(*f.order, "mode")
	}
	return ModeState{EmergencyDisabled: true}, f.err
}

func TestFeatureEnableAuditFailureResumesNoRuntime(t *testing.T) {
	installer := &featureInstallerFake{}
	lifecycle := &FeatureLifecycle{
		installer: installer, writes: NewWriteFence(), timeout: time.Second,
		auditor: &authorityAuditFake{err: errors.New("database-SENTINEL")},
	}
	err := lifecycle.Enable(context.Background(), "actor-a")
	if err == nil || strings.Contains(err.Error(), "database-SENTINEL") {
		t.Fatalf("feature audit failure was not bounded: %v", err)
	}
	if installer.resumeCalls != 0 {
		t.Fatalf("feature audit failure resumed runtime %d times", installer.resumeCalls)
	}
}

func TestFeatureEnableMaterializesRuntimeWithoutRestart(t *testing.T) {
	runtime := &featureRuntimeFake{}
	lifecycle := &FeatureLifecycle{
		connection: func(context.Context) (sqlc.CharlieConnection, error) {
			return sqlc.CharlieConnection{Active: true, HealthState: "ready"}, nil
		},
		runtime: runtime, writes: NewWriteFence(), timeout: time.Second, auditor: &authorityAuditFake{},
	}
	if err := lifecycle.Enable(t.Context(), "actor-a"); err != nil {
		t.Fatal(err)
	}
	if runtime.activations != 1 {
		t.Fatalf("runtime activations=%d", runtime.activations)
	}
}

func TestFeatureDisableQuiescesBeforeAuthorizedAgentSuspension(t *testing.T) {
	order := []string{}
	runtime := &featureRuntimeFake{order: &order}
	installer := &featureInstallerFake{order: &order}
	lifecycle := &FeatureLifecycle{
		connection: func(context.Context) (sqlc.CharlieConnection, error) {
			return sqlc.CharlieConnection{ID: uuid.New(), InstallationID: uuid.New(), Active: true, HealthState: "ready"}, nil
		},
		installer: installer, mode: featureModeFake{order: &order, err: errors.New("Charlie is locally disabled; remote confirmation is pending")}, runtime: runtime,
		writes: NewWriteFence(), timeout: time.Second, auditor: &authorityAuditFake{},
	}
	if err := lifecycle.Disable(t.Context(), "actor-a"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, ","); got != "mode,runtime,suspend" {
		t.Fatalf("disable order=%s", got)
	}
	if installer.suspendCalls != 1 || runtime.shutdowns != 1 {
		t.Fatalf("suspends=%d shutdowns=%d", installer.suspendCalls, runtime.shutdowns)
	}
}
