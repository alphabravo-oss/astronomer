//go:build linux

package upgrade

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func checkpointFixture(t *testing.T) (string, Plan, Checkpoint) {
	t.Helper()
	root := t.TempDir()
	if os.Chmod(root, 0o700) != nil {
		t.Fatal("protect checkpoint directory")
	}
	plan := testPlan()
	state := Checkpoint{SchemaVersion: 1, Target: plan.Target, PlanDigest: planDigest(plan), Identity: plan.Identity, Completed: []Stage{}, Pending: Verify}
	return root, plan, state
}
func TestPrivateCheckpointPersistsIntentAndRetainsAcrossClose(t *testing.T) {
	root, plan, state := checkpointFixture(t)
	store, err := OpenFileStore(root, plan)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background())
	if err != nil || loaded == nil || loaded.Pending != Verify || loaded.PlanDigest != state.PlanDigest {
		t.Fatal("durable private intent differs")
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	// Resume gets the same private intent; closing never discards recovery.
	reopened, err := OpenFileStore(root, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	loaded, err = reopened.Load(context.Background())
	if err != nil || loaded == nil || loaded.Pending != Verify {
		t.Fatal("retained checkpoint lost")
	}
	stat, err := os.Lstat(filepath.Join(root, "checkpoint.json"))
	if err != nil || stat.Mode().Perm() != 0o600 {
		t.Fatal("checkpoint permission differs")
	}
}
func TestPrivateCheckpointFencesConcurrentAndRecreatedOwners(t *testing.T) {
	root, plan, state := checkpointFixture(t)
	store, err := OpenFileStore(root, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	another, err := OpenFileStore(root, plan)
	if err == nil {
		_ = another.Close()
		t.Fatal("second runner admitted")
	}
	if err = store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(root, "checkpoint.json"))
	if err != nil {
		t.Fatal("original checkpoint missing")
	}
	old := root + "-original"
	if err = os.Rename(root, old); err != nil {
		t.Fatal("recreated fixture unavailable")
	}
	t.Cleanup(func() { _ = os.RemoveAll(root); _ = os.Rename(old, root) })
	if os.Mkdir(root, 0o700) != nil {
		t.Fatal("new directory unavailable")
	}
	if err = store.Save(context.Background(), state); err == nil {
		t.Fatal("recreated directory acknowledged")
	}
	retained, err := os.ReadFile(filepath.Join(old, "checkpoint.json"))
	if err != nil || !bytes.Equal(retained, original) {
		t.Fatal("old owner's recovery changed")
	}
	if _, err = os.Stat(filepath.Join(root, "checkpoint.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("new owner received stale checkpoint")
	}
}
func TestPrivateCheckpointRejectsCorruptAndContradictoryResume(t *testing.T) {
	for _, kind := range []string{"duplicate", "unknown", "null", "unsupported_schema", "contradictory", "different_plan", "oversized", "symlink", "public_mode"} {
		t.Run(kind, func(t *testing.T) {
			root, plan, state := checkpointFixture(t)
			store, err := OpenFileStore(root, plan)
			if err != nil {
				t.Fatal(err)
			}
			if store.Save(context.Background(), state) != nil || store.Close() != nil {
				t.Fatal("write baseline checkpoint")
			}
			path := filepath.Join(root, "checkpoint.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal("read baseline")
			}
			switch kind {
			case "duplicate":
				raw = bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":1,"schema_version":1`), 1)
			case "unknown":
				raw = bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":1,"future_field":true`), 1)
			case "null":
				raw = bytes.Replace(raw, []byte(`"applied":false`), []byte(`"applied":null`), 1)
			case "unsupported_schema":
				raw = bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":2`), 1)
			case "contradictory":
				raw = bytes.Replace(raw, []byte(`"applied":false`), []byte(`"applied":true`), 1)
			case "different_plan":
				plan.Mode = Preview
			case "oversized":
				raw = bytes.Repeat([]byte(" "), checkpointLimit+1)
			case "symlink":
				if os.Rename(path, path+"-retained") != nil || os.Symlink(path+"-retained", path) != nil {
					t.Fatal("alias fixture unavailable")
				}
			case "public_mode":
				if os.Chmod(path, 0o644) != nil {
					t.Fatal("mode fixture unavailable")
				}
			}
			if kind != "symlink" && kind != "public_mode" && os.WriteFile(path, raw, 0o600) != nil {
				t.Fatal("write malformed fixture")
			}
			reopened, err := OpenFileStore(root, plan)
			if err == nil {
				_ = reopened.Close()
				t.Fatal("unsafe checkpoint admitted for resume")
			}
		})
	}
}
func TestPrivateCheckpointFailedPublicationPreservesAcknowledgedBytes(t *testing.T) {
	for _, kind := range []string{"cancelled", "interrupted_pending", "changed_lock"} {
		t.Run(kind, func(t *testing.T) {
			root, plan, state := checkpointFixture(t)
			store, err := OpenFileStore(root, plan)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = store.Close() }()
			if store.Save(context.Background(), state) != nil {
				t.Fatal("write baseline")
			}
			before, err := os.ReadFile(filepath.Join(root, "checkpoint.json"))
			if err != nil {
				t.Fatal("read baseline")
			}
			next := state
			next.Completed = []Stage{Verify}
			next.Pending = ""
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "cancelled":
				cancel()
			case "interrupted_pending":
				if os.WriteFile(filepath.Join(root, "checkpoint.pending"), []byte("synthetic interrupted private intent"), 0o600) != nil {
					t.Fatal("write prior intent")
				}
			case "changed_lock":
				if os.Rename(filepath.Join(root, "checkpoint.lock"), filepath.Join(root, "checkpoint.lock-retained")) != nil || os.WriteFile(filepath.Join(root, "checkpoint.lock"), []byte("synthetic new owner"), 0o600) != nil {
					t.Fatal("recreate lock")
				}
			}
			if store.Save(ctx, next) == nil {
				t.Fatal("failed publication acknowledged")
			}
			after, err := os.ReadFile(filepath.Join(root, "checkpoint.json"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("last acknowledged checkpoint changed")
			}
			if kind == "interrupted_pending" {
				pending, e := os.ReadFile(filepath.Join(root, "checkpoint.pending"))
				if e != nil || string(pending) != "synthetic interrupted private intent" {
					t.Fatal("interrupted recovery bytes overwritten")
				}
			}
		})
	}
}

func TestPrivateCheckpointCannotRewindAcknowledgedProgress(t *testing.T) {
	root, plan, state := checkpointFixture(t)
	store, err := OpenFileStore(root, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if store.Save(context.Background(), state) != nil {
		t.Fatal("write initial intent")
	}
	progressed := state
	progressed.Completed = []Stage{Verify}
	progressed.Pending = ""
	if store.Save(context.Background(), progressed) != nil {
		t.Fatal("acknowledge completed verify")
	}
	if store.Save(context.Background(), state) == nil {
		t.Fatal("rewound acknowledged stage")
	}
	if _, err = store.Load(nil); err == nil || store.Save(nil, progressed) == nil {
		t.Fatal("nil checkpoint context admitted")
	}
}

func TestPrivateCheckpointDoesNotOverwriteUnexpectedRecoveryFile(t *testing.T) {
	root, plan, state := checkpointFixture(t)
	store, err := OpenFileStore(root, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	path := filepath.Join(root, "checkpoint.json")
	sentinel := []byte("synthetic recovery appeared after admission")
	if os.WriteFile(path, sentinel, 0o600) != nil {
		t.Fatal("write new recovery fixture")
	}
	if store.Save(context.Background(), state) == nil {
		t.Fatal("unexpected recovery overwritten")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, sentinel) {
		t.Fatal("unobserved recovery bytes changed")
	}
}
