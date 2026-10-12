//go:build linux

package upgradecli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"text/template"
)

func readinessFixture(t *testing.T) (*Readiness, *[]string) {
	t.Helper()
	fence, _ := fenceFixture(t)
	read := fence.execute
	seen := []string{}
	fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		if args[4] == "auth" {
			seen = append(seen, "permission")
			return []byte("yes\n"), nil
		}
		if args[4] == "get" {
			switch args[5] {
			case "nodes":
				seen = append(seen, "nodes")
				return []byte("1\n0\n"), nil
			case "deployments", "poddisruptionbudgets":
				if args[6] != "--namespace" || args[7] != fence.options.Identity.Namespace || args[8] != "--selector" || args[9] != "app.kubernetes.io/instance=astronomer,app.kubernetes.io/part-of=astronomer" {
					t.Fatal("legacy deployment/PDB scope changed")
				}
				seen = append(seen, args[5])
				if args[5] == "deployments" {
					return []byte("4\t4\t2\t2\t2\n"), nil
				}
				return []byte("1\n"), nil
			}
		}
		return read(ctx, tool, args)
	}
	r, err := NewReadiness(fence, 1)
	if err != nil {
		t.Fatal(err)
	}
	return r, &seen
}

func TestReadinessUsesOwnedLegacyResourceScopes(t *testing.T) {
	r, seen := readinessFixture(t)
	if err := r.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(*seen, ",") != "permission,nodes,deployments,poddisruptionbudgets" {
		t.Fatal("preconditions missing or reordered")
	}
	if _, err := NewReadiness(r.fence, 0); err == nil {
		t.Fatal("zero node minimum admitted")
	}
	if err := r.Check(nil); err == nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
		t.Fatal("nil context admitted")
	}
}

func TestReadinessRejectsFailedOrIncompleteResourceObservations(t *testing.T) {
	for _, resource := range []string{"permission", "nodes", "deployments", "poddisruptionbudgets"} {
		t.Run(resource, func(t *testing.T) {
			r, _ := readinessFixture(t)
			read := r.fence.execute
			r.fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
				if (resource == "permission" && args[4] == "auth") || (args[4] == "get" && args[5] == resource) {
					return []byte("private response must not reach diagnostic"), errors.New("synthetic joined read failure")
				}
				return read(ctx, tool, args)
			}
			if err := r.Check(context.Background()); err == nil {
				t.Fatal("failed observation qualified readiness")
			}
		})
	}
}

func TestReadinessRejectsFinalCancellationAndOwnerLoss(t *testing.T) {
	for _, change := range []string{"cancel", "recreated"} {
		t.Run(change, func(t *testing.T) {
			r, _ := readinessFixture(t)
			read := r.fence.execute
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			finished := false
			r.fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
				raw, err := read(ctx, tool, args)
				if args[4] == "get" && args[5] == "poddisruptionbudgets" {
					finished = true
					if change == "cancel" {
						cancel()
					}
				}
				if finished && change == "recreated" && args[4] == "get" && args[5] == "namespace" {
					raw = []byte("new-uid\towned-namespace\tlive")
				}
				return raw, err
			}
			err := r.Check(ctx)
			if err == nil || (change == "cancel" && !errors.Is(err, context.Canceled)) {
				t.Fatal("expired or recreated owner readiness acknowledged")
			}
		})
	}
}

func TestReadinessProjectionsRetainLegacyGatesAndFiniteBounds(t *testing.T) {
	for _, row := range []struct {
		name  string
		raw   string
		check func([]byte) error
		pass  bool
	}{
		{"nodes-ready", "1\n0\n", func(raw []byte) error { return checkReadyNodes(raw, 1) }, true},
		{"nodes-cordoned", "0\n", func(raw []byte) error { return checkReadyNodes(raw, 1) }, false},
		{"nodes-malformed", "yes\n", func(raw []byte) error { return checkReadyNodes(raw, 1) }, false},
		{"deployment-ready", "4\t4\t2\t2\t2\n", checkReadyDeployments, true},
		{"deployment-zero-replicas", "4\t4\t0\t0\t0\n", checkReadyDeployments, true},
		{"deployment-empty", "", checkReadyDeployments, false},
		{"deployment-stale", "4\t3\t2\t2\t2\n", checkReadyDeployments, false},
		{"deployment-unavailable", "4\t4\t2\t1\t2\n", checkReadyDeployments, false},
		{"deployment-not-updated", "4\t4\t2\t2\t1\n", checkReadyDeployments, false},
		{"deployment-negative", "4\t4\t-1\t2\t2\n", checkReadyDeployments, false},
		{"deployment-overflow", "18446744073709551616\t4\t2\t2\t2\n", checkReadyDeployments, false},
		{"pdb-empty-legacy", "", checkDisruptionAllowance, true},
		{"pdb-permits", "1\n2\n", checkDisruptionAllowance, true},
		{"pdb-denies", "1\n0\n", checkDisruptionAllowance, false},
		{"pdb-leading-zero", "01\n", checkDisruptionAllowance, false},
		{"row-budget", strings.Repeat("1\n", readinessRowsLimit+1), checkDisruptionAllowance, false},
		{"row-no-terminator", "1", checkDisruptionAllowance, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			if (row.check([]byte(row.raw)) == nil) != row.pass {
				t.Fatal("readiness prerequisite or finite parser contract differs")
			}
		})
	}
}

func TestReadinessExecutesCanonicalKubectlTemplates(t *testing.T) {
	condition := func(ready string) map[string]any { return map[string]any{"type": "Ready", "status": ready} }
	fixtures := []struct {
		source string
		items  []map[string]any
		want   string
	}{
		{readyNodesTemplate, []map[string]any{
			{"spec": map[string]any{}, "status": map[string]any{"conditions": []map[string]any{condition("True")}}},
			{"spec": map[string]any{"unschedulable": true}, "status": map[string]any{"conditions": []map[string]any{condition("True")}}},
			{"spec": map[string]any{}, "status": map[string]any{"conditions": []map[string]any{condition("False")}}},
		}, "1\n0\n0\n"},
		{deploymentsReadyTemplate, []map[string]any{{"metadata": map[string]any{"generation": 4}, "spec": map[string]any{"replicas": 2}, "status": map[string]any{"observedGeneration": 4, "availableReplicas": 2, "updatedReplicas": 2}}}, "4\t4\t2\t2\t2\n"},
		{disruptionTemplate, []map[string]any{{"status": map[string]any{"disruptionsAllowed": 2}}, {"status": map[string]any{}}}, "2\n0\n"},
	}
	for _, fixture := range fixtures {
		tpl, err := template.New("actual-kubectl-template").Parse(fixture.source)
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		if err := tpl.Execute(&output, map[string]any{"items": fixture.items}); err != nil {
			t.Fatal(err)
		}
		if output.String() != fixture.want {
			t.Fatal("actual template differs from parsed prerequisite")
		}
	}
}
