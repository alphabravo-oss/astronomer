//go:build linux

package upgradecli

import (
	"context"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

func TestReleaseDriverConstructorRejectsUnboundAndPartiallyForgedAdapters(t *testing.T) {
	for _, verification := range []*ReleaseVerification{nil, {}, {chart: &TargetChartContent{}}} {
		if _, err := NewReleaseDriver(context.Background(), verification, &ReleaseBackup{}, &TargetWorkloads{}); err == nil {
			t.Fatal("missing authenticated composition admitted")
		}
	}
	if _, err := NewReleaseDriver(nil, nil, nil, nil); err == nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
		t.Fatal("nil context admitted")
	}
}

func TestReleaseDriverRequiresExplicitContextAtEveryBoundary(t *testing.T) {
	var driver *ReleaseDriver
	if driver.Fence(context.Background(), upgrade.Identity{}, nil) == nil {
		t.Fatal("nil Driver fenced ownership")
	}
	if _, err := driver.Step(context.Background(), upgrade.Apply, upgrade.Plan{}); err == nil {
		t.Fatal("nil Driver applied")
	}
	if _, err := driver.Reconcile(context.Background(), upgrade.Checkpoint{}); err == nil {
		t.Fatal("nil Driver reconciled")
	}
	if driver.Cleanup(context.Background(), upgrade.Plan{}, upgrade.CleanupPolicy{}) == nil {
		t.Fatal("nil Driver cleaned resources")
	}
	partial := &ReleaseDriver{}
	if partial.Fence(context.Background(), upgrade.Identity{}, nil) == nil {
		t.Fatal("zero Driver fenced ownership")
	}
	if _, err := partial.Step(context.Background(), upgrade.Apply, upgrade.Plan{}); err == nil {
		t.Fatal("zero Driver applied")
	}
	if _, err := partial.Reconcile(context.Background(), upgrade.Checkpoint{}); err == nil {
		t.Fatal("zero Driver reconciled")
	}
	if partial.Cleanup(context.Background(), upgrade.Plan{}, upgrade.CleanupPolicy{OwnedEphemeralOnly: true, PreserveBackup: true, PreserveCheckpoint: true}) == nil {
		t.Fatal("zero Driver cleaned resources")
	}
	if partial.Fence(nil, upgrade.Identity{}, nil) == nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
		t.Fatal("nil context fenced ownership")
	}
	if _, err := partial.Step(nil, upgrade.Apply, upgrade.Plan{}); err == nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
		t.Fatal("nil context applied")
	}
	if _, err := partial.Reconcile(nil, upgrade.Checkpoint{}); err == nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
		t.Fatal("nil context reconciled")
	}
	if partial.Cleanup(nil, upgrade.Plan{}, upgrade.CleanupPolicy{}) == nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
		t.Fatal("nil context cleaned resources")
	}
}

// Tag admission is only a veto against accidental implicit major changes.
// These parser cases do not authenticate image contents or execute upgrades.
func TestReleaseDriverPostgresMajorVetoRequiresClosedDeclaredTag(t *testing.T) {
	for raw, expected := range map[string]uint32{"postgres:17.11-alpine": 17, "docker.io/library/postgres:18.6": 18, "registry.example/postgres:17-alpine": 17} {
		actual, err := declaredPostgresMajor(raw)
		if err != nil || actual != expected {
			t.Fatal("declared source major lost", err)
		}
	}
	for _, raw := range []string{"", "postgres", "postgres:latest", "postgres:v17.11", "postgres:017.11", "postgres:0.1", "postgres:17rc1", "postgres:4294967296.1", "postgres@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "postgres:17.11@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		if _, err := declaredPostgresMajor(raw); err == nil {
			t.Fatal("ambiguous runtime major admitted")
		}
	}
}
