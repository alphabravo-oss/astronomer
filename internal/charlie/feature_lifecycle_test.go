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
	runtime := &featureRuntimeFake{}
	lifecycle := &FeatureLifecycle{
		runtime: runtime, writes: NewWriteFence(), timeout: time.Second,
		auditor: &authorityAuditFake{err: errors.New("database-SENTINEL")},
	}
	err := lifecycle.Enable(context.Background(), "actor-a")
	if err == nil || strings.Contains(err.Error(), "database-SENTINEL") {
		t.Fatalf("feature audit failure was not bounded: %v", err)
	}
	if runtime.activations != 0 {
		t.Fatalf("feature audit failure activated runtime %d times", runtime.activations)
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

func TestFeatureDisableQuiescesBeforeRuntimeShutdown(t *testing.T) {
	order := []string{}
	runtime := &featureRuntimeFake{order: &order}
	lifecycle := &FeatureLifecycle{
		connection: func(context.Context) (sqlc.CharlieConnection, error) {
			return sqlc.CharlieConnection{ID: uuid.New(), InstallationID: uuid.New(), Active: true, HealthState: "ready"}, nil
		},
		mode: featureModeFake{order: &order, err: errors.New("Charlie is locally disabled; remote confirmation is pending")}, runtime: runtime,
		writes: NewWriteFence(), timeout: time.Second, auditor: &authorityAuditFake{},
	}
	if err := lifecycle.Disable(t.Context(), "actor-a"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, ","); got != "mode,runtime" {
		t.Fatalf("disable order=%s", got)
	}
	if runtime.shutdowns != 1 {
		t.Fatalf("shutdowns=%d", runtime.shutdowns)
	}
}

func TestFeatureDisableStopsRuntimeForInactiveConnection(t *testing.T) {
	runtime := &featureRuntimeFake{}
	lifecycle := &FeatureLifecycle{
		connection: func(context.Context) (sqlc.CharlieConnection, error) {
			return sqlc.CharlieConnection{ID: uuid.New(), InstallationID: uuid.New(), Active: false, HealthState: "inactive"}, nil
		},
		runtime: runtime, writes: NewWriteFence(), timeout: time.Second, auditor: &authorityAuditFake{},
	}
	if err := lifecycle.Disable(t.Context(), "actor-a"); err != nil {
		t.Fatal(err)
	}
	if runtime.shutdowns != 1 {
		t.Fatalf("inactive shutdowns=%d", runtime.shutdowns)
	}
}
