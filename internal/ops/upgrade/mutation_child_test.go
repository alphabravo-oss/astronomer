//go:build linux

package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

// Executed as an actual owned subprocess. It writes a synthetic durable effect
// and never acknowledges it; the typed process runner must terminate it.
func TestOwnedUpgradeMutationHelper(t *testing.T) {
	if os.Getenv("OPS_UPGRADE_MUTATION_HELPER") != "1" {
		return
	}
	if os.WriteFile(os.Getenv("OPS_UPGRADE_MARKER"), []byte("synthetic committed effect"), 0o600) != nil {
		os.Exit(2)
	}
	for {
		time.Sleep(time.Second)
	}
}

type privateTestCheckpoint struct {
	path  string
	state Checkpoint
}

func (s *privateTestCheckpoint) Save(ctx context.Context, state Checkpoint) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.state = state
	bytes, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, bytes, 0o600)
}

type mutationChildDriver struct {
	root, marker, backup, owner   string
	known, recreate, failRollback bool
	cleanupCalls                  int
	spec                          process.Spec
}

func (d *mutationChildDriver) Fence(ctx context.Context, id Identity, _ *DisposableLease) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	bytes, err := os.ReadFile(d.owner)
	if err != nil || string(bytes) != id.NamespaceUID {
		return errors.New("synthetic owned resource changed")
	}
	return nil
}
func (d *mutationChildDriver) Step(ctx context.Context, stage Stage, _ Plan) (Outcome, error) {
	switch stage {
	case Apply:
		_, err := process.Run(ctx, d.spec)
		if d.recreate {
			if os.WriteFile(d.owner, []byte("recreated-owner"), 0o600) != nil {
				return Outcome{}, errors.New("write recreated fixture")
			}
		}
		return Outcome{}, err
	case Rollback:
		if d.failRollback {
			return Outcome{Known: true}, childFailure(43)
		}
		if os.Remove(d.marker) != nil {
			return Outcome{Known: true}, errors.New("remove synthetic committed effect")
		}
	}
	return Outcome{Known: true}, nil
}
func (d *mutationChildDriver) Reconcile(ctx context.Context, state Checkpoint) (Outcome, error) {
	if err := d.Fence(ctx, state.Identity, nil); err != nil {
		return Outcome{}, err
	}
	_, err := os.Stat(d.marker)
	return Outcome{Known: d.known, Applied: err == nil}, nil
}
func (d *mutationChildDriver) Cleanup(ctx context.Context, _ Plan, policy CleanupPolicy) error {
	d.cleanupCalls++
	if ctx.Err() != nil || !policy.OwnedEphemeralOnly || !policy.PreserveBackup || !policy.PreserveCheckpoint {
		return errors.New("private recovery retention required")
	}
	// The synthetic effect and private recovery files are not ephemeral handles.
	return nil
}

func TestTerminatedMutationBeforeAcknowledgementPreservesRecoveryByOwnership(t *testing.T) {
	for _, kind := range []string{"unknown", "recreated_owner", "known_applied", "failed_rollback"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			if os.Chmod(root, 0o700) != nil {
				t.Fatal("protect owned fixture")
			}
			plan := testPlan()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal("locate controlled test executable")
			}
			d := &mutationChildDriver{root: root, marker: filepath.Join(root, "committed-effect"), backup: filepath.Join(root, "backup"), owner: filepath.Join(root, "owner"), known: kind == "known_applied" || kind == "failed_rollback", recreate: kind == "recreated_owner", failRollback: kind == "failed_rollback"}
			for path, value := range map[string]string{d.backup: "synthetic backup retained", d.owner: plan.Identity.NamespaceUID} {
				if os.WriteFile(path, []byte(value), 0o600) != nil {
					t.Fatal("write private synthetic fixture")
				}
			}
			d.spec = process.Spec{Executable: executable, Args: []string{"-test.run=^TestOwnedUpgradeMutationHelper$"}, Environment: []process.Environment{{Name: "OPS_UPGRADE_MUTATION_HELPER", Literal: "1"}, {Name: "OPS_UPGRADE_MARKER", Literal: d.marker}}, Directory: root, CaptureDirectory: root, ID: "apply", Timeout: time.Second, TerminationGrace: 100 * time.Millisecond, MaximumCaptureBytes: 4096}
			store := &privateTestCheckpoint{path: filepath.Join(root, "checkpoint")}
			result := (Machine{Driver: d, Store: store}).Run(context.Background(), plan, nil)
			if result.ExitCode != 124 {
				t.Fatalf("owned child did not time out before acknowledgement: %+v", result)
			}
			bytes, err := os.ReadFile(d.backup)
			if err != nil || string(bytes) != "synthetic backup retained" {
				t.Fatal("recovery backup removed or changed")
			}
			if _, err = os.Stat(store.path); err != nil {
				t.Fatal("private intent checkpoint removed")
			}
			if kind == "unknown" || kind == "recreated_owner" {
				if result.Result != "outcome_unknown" || d.cleanupCalls != 0 || result.RollbackAttempted {
					t.Fatalf("unresolved/recreated mutation treated as safely cleanable: %+v", result)
				}
				if _, err = os.Stat(d.marker); err != nil {
					t.Fatal("unknown mutation marker or recovery state discarded")
				}
			} else {
				if !result.RollbackAttempted || result.RollbackFailed != d.failRollback || d.cleanupCalls != 1 {
					t.Fatalf("observed mutation recovery differs: %+v", result)
				}
				if d.failRollback {
					if _, err = os.Stat(d.marker); err != nil {
						t.Fatal("failed rollback effect evidence removed")
					}
				}
			}
		})
	}
}
