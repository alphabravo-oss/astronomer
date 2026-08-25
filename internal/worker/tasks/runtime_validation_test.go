package tasks

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestValidateRequiredRuntimeDependenciesReportsSortedMissingAndTypedNil(t *testing.T) {
	var typedNil *stubRuntimeDependency
	err := validateRequiredRuntimeDependencies("test worker", []requiredRuntimeDependency{
		{name: "zeta", value: nil},
		{name: "ready", value: &stubRuntimeDependency{}},
		{name: "alpha", value: typedNil},
	})
	if err == nil {
		t.Fatal("expected incomplete runtime error")
	}
	if got := err.Error(); !strings.Contains(got, "missing: alpha, zeta") {
		t.Fatalf("error = %q, want stable sorted missing dependency list", got)
	}
}

func TestValidateRequiredRuntimeDependenciesAcceptsCompleteGraph(t *testing.T) {
	if err := validateRequiredRuntimeDependencies("test worker", []requiredRuntimeDependency{
		{name: "function", value: func() {}},
		{name: "pointer", value: &stubRuntimeDependency{}},
		{name: "value", value: 1},
	}); err != nil {
		t.Fatalf("validate complete graph: %v", err)
	}
}

type stubRuntimeDependency struct{}

type notReadyManagementBackup struct{}

func (notReadyManagementBackup) ManagementBackupReady() bool { return false }
func (notReadyManagementBackup) ReconcileManagementBackup(context.Context, uuid.UUID, int64) error {
	return nil
}
func (notReadyManagementBackup) ExecuteManagementBackupOperation(context.Context, uuid.UUID) error {
	return nil
}

func TestManagementBackupRuntimeDependencyRejectsPresentButUnreadyExecutor(t *testing.T) {
	if got := managementBackupRuntimeDependency(notReadyManagementBackup{}); got != nil {
		t.Fatalf("unready executor passed runtime readiness: %#v", got)
	}
}
