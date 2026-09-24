package tasks

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/gitops"
)

func clusterRegistrationYAMLWithPresets(name string, registries, toolPresets []string) string {
	y := "apiVersion: astronomer.alphabravo.io/v1\nkind: ClusterRegistration\nmetadata:\n  name: " + name + "\nspec:\n"
	if len(registries) > 0 {
		y += "  registries:\n"
		for _, r := range registries {
			y += "    - " + r + "\n"
		}
	}
	if len(toolPresets) > 0 {
		y += "  toolPresets:\n"
		for _, tp := range toolPresets {
			y += "    - " + tp + "\n"
		}
	}
	return y
}

// Unsupported desired state must stop the complete source before any cluster
// mutation, even if another document in the same source is supported.
func TestSync_RejectsDeclaredPresetsBeforeApplyingAnyCluster(t *testing.T) {
	q := newFakeQuerier()
	bare, work := makeBareRepo(t)
	if err := writeCommit(t, work, "clusters/prod-east.yaml",
		clusterRegistrationYAMLWithPresets("prod-east", []string{"harbor"}, []string{"cert-manager-prod"}), "add"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := writeCommit(t, work, "clusters/aaa-valid.yaml", clusterRegistrationYAML("valid", nil), "add valid"); err != nil {
		t.Fatal(err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push: %v", err)
	}
	src := setupSource(t, q, bare, "log", "interval")
	runtime := GitOpsRuntime{Deps: GitOpsDeps{Queries: q, CloneRoot: t.TempDir(), Now: time.Now}}

	err := runtime.SyncSource(context.Background(), src.ID)
	if !errors.Is(err, gitops.ErrUnsupportedIntent) || !strings.Contains(err.Error(), "spec.registries") || !strings.Contains(err.Error(), "spec.toolPresets") {
		t.Fatalf("expected explicit unsupported-intent error, got %v", err)
	}
	if len(q.clusters) != 0 {
		t.Fatalf("unsupported source applied %d clusters", len(q.clusters))
	}
}

// TestSync_NoPresetsNoSurfaceAudit guards against the surfacing firing on
// every sync: a registration with no registries/toolPresets must not emit the
// unreconciled audit.
func TestSync_NoPresetsNoSurfaceAudit(t *testing.T) {
	q := newFakeQuerier()
	bare, work := makeBareRepo(t)
	if err := writeCommit(t, work, "clusters/prod-east.yaml", clusterRegistrationYAML("prod-east", nil), "add"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push: %v", err)
	}
	src := setupSource(t, q, bare, "log", "interval")
	runtime := GitOpsRuntime{Deps: GitOpsDeps{Queries: q, CloneRoot: t.TempDir(), Now: time.Now}}

	if err := runtime.SyncSource(context.Background(), src.ID); err != nil {
		t.Fatalf("SyncSource: %v", err)
	}
	if containsAction(q.auditRows, "gitops.cluster.presets_unreconciled") {
		t.Fatalf("must not surface presets audit when none are declared")
	}
}
