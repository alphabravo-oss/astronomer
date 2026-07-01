package tasks

import (
	"context"
	"strings"
	"testing"
	"time"
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

// auditDetailFor returns the JSON detail bytes of the first audit row with the
// given action, or nil.
func auditDetailFor(q *fakeGitOpsQuerier, action string) []byte {
	for _, r := range q.auditRows {
		if r.Action == action {
			return r.Detail
		}
	}
	return nil
}

// TestSync_SurfacesDeclaredPresets is the regression for the silently-dropped
// spec.registries / spec.toolPresets: a successful sync of a doc that declares
// them must NOT look like it applied them. Until the downstream reconcile is
// wired, the worker surfaces the declared names via a warn log + audit event
// so an operator is never misled by a green sync.
func TestSync_SurfacesDeclaredPresets(t *testing.T) {
	ResetGitOps()
	defer ResetGitOps()
	q := newFakeQuerier()
	bare, work := makeBareRepo(t)
	if err := writeCommit(t, work, "clusters/prod-east.yaml",
		clusterRegistrationYAMLWithPresets("prod-east", []string{"harbor"}, []string{"cert-manager-prod"}), "add"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push: %v", err)
	}
	src := setupSource(t, q, bare, "log", "interval")
	ConfigureGitOps(GitOpsDeps{Queries: q, CloneRoot: t.TempDir(), Now: time.Now})

	if err := SyncSource(context.Background(), src.ID); err != nil {
		t.Fatalf("SyncSource: %v", err)
	}
	detail := auditDetailFor(q, "gitops.cluster.presets_unreconciled")
	if detail == nil {
		t.Fatalf("expected gitops.cluster.presets_unreconciled audit for declared registries/toolPresets; got actions %+v", q.auditRows)
	}
	if !strings.Contains(string(detail), "harbor") || !strings.Contains(string(detail), "cert-manager-prod") {
		t.Fatalf("audit detail should name the dropped registries/toolPresets; got %s", string(detail))
	}
}

// TestSync_NoPresetsNoSurfaceAudit guards against the surfacing firing on
// every sync: a registration with no registries/toolPresets must not emit the
// unreconciled audit.
func TestSync_NoPresetsNoSurfaceAudit(t *testing.T) {
	ResetGitOps()
	defer ResetGitOps()
	q := newFakeQuerier()
	bare, work := makeBareRepo(t)
	if err := writeCommit(t, work, "clusters/prod-east.yaml", clusterRegistrationYAML("prod-east", nil), "add"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push: %v", err)
	}
	src := setupSource(t, q, bare, "log", "interval")
	ConfigureGitOps(GitOpsDeps{Queries: q, CloneRoot: t.TempDir(), Now: time.Now})

	if err := SyncSource(context.Background(), src.ID); err != nil {
		t.Fatalf("SyncSource: %v", err)
	}
	if containsAction(q.auditRows, "gitops.cluster.presets_unreconciled") {
		t.Fatalf("must not surface presets audit when none are declared")
	}
}
