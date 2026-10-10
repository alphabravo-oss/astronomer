//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type quiescedFixtureState struct {
	serverVersion, pdbVersion, pdbUID, dependencyAllowance, readyNode, permission string
	serverReplicas                                                                string
	foreignSelector                                                               bool
}

func quiescedReadinessFixture(t *testing.T) (*Readiness, *DisposableReplacement, *quiescedFixtureState) {
	t.Helper()
	replacement, mode := replacementFixture(t)
	*mode = "renamed"
	replacement.replaced = true
	state := &quiescedFixtureState{serverVersion: "20", pdbVersion: "30", pdbUID: "server-pdb-original", dependencyAllowance: "1", readyNode: "1", permission: "yes", serverReplicas: "0"}
	fence := replacement.backup.source.fence
	original := fence.execute
	id := fence.options.Identity
	fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		if args[4] == "auth" {
			return []byte(state.permission + "\n"), nil
		}
		if args[4] == "get" {
			switch args[5] {
			case "nodes":
				return []byte(state.readyNode + "\n"), nil
			case "deployments":
				if args[len(args)-1] == "go-template="+replacementDeploymentsTemplate {
					return []byte("owned-server\tserver-original\t" + id.Namespace + "\tserver\t" + state.serverReplicas + "\t" + state.serverVersion + "\tlive\nowned-worker\tworker-original\t" + id.Namespace + "\tworker\t0\t21\tlive\n"), nil
				}
				return []byte("2\t2\t0\t0\t0\n3\t3\t1\t1\t1\n"), nil
			case "poddisruptionbudgets":
				if args[len(args)-1] == "go-template="+disruptionTemplate {
					return []byte("0\n" + state.dependencyAllowance + "\n"), nil
				}
				release := id.Release
				if state.foreignSelector {
					release = "foreign"
				}
				return []byte("owned-server\t" + state.pdbUID + "\t" + id.Namespace + "\t" + state.pdbVersion + "\tlive\t0\t3\t0\tastronomer\t" + release + "\tserver\nowned-frontend\tfrontend-pdb\t" + id.Namespace + "\t40\tlive\t" + state.dependencyAllowance + "\t3\t0\tastronomer\t" + id.Release + "\tfrontend\n"), nil
			}
		}
		return original(ctx, tool, args)
	}
	replacement.deployments = []replacementDeployment{{"owned-server", "server-original", "server", "10", 2}, {"owned-worker", "worker-original", "worker", "11", 1}}
	if err := replacement.capturePDBOwners(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := replacement.captureQuiesced(context.Background()); err != nil {
		t.Fatal(err)
	}
	ready, err := NewReadiness(fence, 1)
	if err != nil {
		t.Fatal(err)
	}
	return ready, replacement, state
}
func TestQuiescedReadinessPermitsOnlyCapturedZeroWorkloadsAndExactOwnedPDB(t *testing.T) {
	ready, replacement, _ := quiescedReadinessFixture(t)
	if err := ready.Check(context.Background()); err == nil {
		t.Fatal("ordinary readiness incorrectly relaxed zero PDB")
	}
	if err := ready.check(context.Background(), replacement); err != nil {
		t.Fatal("owned quiesced stage blocked", err)
	}
}
func TestQuiescedReadinessRejectsChangedOwnershipAndOtherDependencies(t *testing.T) {
	for _, changed := range []string{"deployment-version", "deployment-scaled", "pdb-version", "pdb-uid", "selector", "dependency", "nodes", "permission", "replacement"} {
		t.Run(changed, func(t *testing.T) {
			ready, replacement, state := quiescedReadinessFixture(t)
			switch changed {
			case "deployment-version":
				state.serverVersion = "22"
			case "deployment-scaled":
				state.serverReplicas = "1"
			case "pdb-version":
				state.pdbVersion = "31"
			case "pdb-uid":
				state.pdbUID = "recreated"
			case "selector":
				state.foreignSelector = true
			case "dependency":
				state.dependencyAllowance = "0"
			case "nodes":
				state.readyNode = "0"
			case "permission":
				state.permission = "no"
			case "replacement":
				replacement.replaced = false
			}
			if err := ready.check(context.Background(), replacement); err == nil {
				t.Fatal("changed ownership or remaining dependency admitted")
			}
		})
	}
}
func TestQuiescedReadinessRejectsCancellationAndMidReadPDBChange(t *testing.T) {
	for _, changed := range []string{"cancel", "pdb-race"} {
		t.Run(changed, func(t *testing.T) {
			ready, replacement, state := quiescedReadinessFixture(t)
			original := ready.fence.execute
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reads := 0
			ready.fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
				raw, err := original(ctx, tool, args)
				if len(args) > 5 && args[4] == "get" && args[5] == "poddisruptionbudgets" {
					reads++
					if reads == 1 {
						if changed == "cancel" {
							cancel()
						} else {
							state.pdbVersion = "31"
						}
					}
				}
				return raw, err
			}
			err := ready.check(ctx, replacement)
			if err == nil || (changed == "cancel" && !errors.Is(err, context.Canceled)) {
				t.Fatal("incomplete owned gate admitted", err)
			}
			if changed == "pdb-race" && !strings.Contains(err.Error(), "version") {
				t.Fatal("wrong race boundary", err)
			}
		})
	}
}
