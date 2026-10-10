//go:build linux

package databasejob

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func syntheticPreparedFullScan(t *testing.T) (string, string, string, Preparation) {
	t.Helper()
	root, coord, value := syntheticPreparedCache(t)
	_, workloadRoot, _, snapshot := workloadInputFixture(t, value.ClaimID)
	value.SchemaVersion, value.Kind, value.Workload = 2, "AstronomerOwnedDatabaseScanPreparation", cloneWorkloadSnapshot(&snapshot)
	// Replace only this test's initially-published schema1 synthetic record. The
	// production publisher still never adopts/overwrites an existing preparation.
	if err := os.Remove(filepath.Join(root, "prepare.json")); err != nil {
		t.Fatal(err)
	}
	if err := PublishPreparation(t.Context(), root, value); err != nil {
		t.Fatal(err)
	}
	return root, workloadRoot, coord, value
}
func TestOwnedScanHelperReadinessAndFinalBindBothPhysicalInputs(t *testing.T) {
	root, workloadRoot, coord, value := syntheticPreparedFullScan(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	final := make(chan struct{})
	writer := &measurementOutput{initial: make(chan struct{})}
	done := make(chan error, 1)
	go func() { defer close(done); done <- ObserveScanInput(ctx, root, workloadRoot, coord, writer, final) }()
	mark := joinObserverCleanup(t, done, cancel)
	if err := WaitInitialMeasurement(ctx, coord, value); err != nil {
		cancel()
		_ = joinedObserver(t, done, cancel)
		mark()
		t.Fatal(err)
	}
	select {
	case <-writer.initial:
	default:
		t.Fatal("ACK precedes completed input measurement")
	}
	select {
	case err := <-done:
		mark()
		t.Fatalf("native sidecar left before final signal: %v", err)
	default:
	}
	close(final)
	err := joinedObserver(t, done, cancel)
	mark()
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(writer.bytes()), []byte{'\n'})
	if len(lines) != 2 {
		t.Fatal("complete input final record unavailable")
	}
	for index, line := range lines {
		row, err := DecodeMeasurement(line, value)
		if err != nil || row.SchemaVersion != 2 || row.Workload == nil || !sameWorkloadSnapshot(row.Workload, value.Workload) {
			t.Fatal("helper lost exact workload input")
		}
		expected := "initial"
		if index == 1 {
			expected = "final"
		}
		if row.Phase != expected {
			t.Fatal("input phases changed")
		}
	}
}
func TestOwnedScanHelperRejectsChangedWorkloadAndLegacyMeasurement(t *testing.T) {
	for _, name := range []string{"changed", "linked", "cancel", "legacy-record", "DB-only-observer"} {
		t.Run(name, func(t *testing.T) {
			root, workloadRoot, coord, value := syntheticPreparedFullScan(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			final := make(chan struct{})
			writer := &measurementOutput{initial: make(chan struct{})}
			done := make(chan error, 1)
			if name == "legacy-record" {
				value.SchemaVersion, value.Kind, value.Workload = 1, "AstronomerDatabaseScanPreparation", nil
				if err := os.Remove(filepath.Join(root, "prepare.json")); err != nil {
					t.Fatal(err)
				}
				if err := PublishPreparation(ctx, root, value); err != nil {
					t.Fatal(err)
				}
			}
			go func() {
				defer close(done)
				if name == "DB-only-observer" {
					done <- ObserveCache(ctx, root, coord, writer, final)
				} else {
					done <- ObserveScanInput(ctx, root, workloadRoot, coord, writer, final)
				}
			}()
			mark := joinObserverCleanup(t, done, cancel)
			if name == "legacy-record" || name == "DB-only-observer" {
				err := joinedObserver(t, done, cancel)
				mark()
				if err == nil || len(writer.bytes()) != 0 {
					t.Fatal("missing full-input route supplied measurement")
				}
				return
			}
			if err := WaitInitialMeasurement(ctx, coord, value); err != nil {
				cancel()
				_ = joinedObserver(t, done, cancel)
				mark()
				t.Fatal(err)
			}
			if name == "cancel" {
				cancel()
			} else {
				member := value.Workload.Members[len(value.Workload.Members)-1]
				path := filepath.Join(workloadRoot, "workload-"+value.ClaimID.String(), member.Name)
				if name == "linked" {
					if err := os.Link(path, filepath.Join(workloadRoot, "foreign-link")); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Chmod(path, 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(strings.Repeat("X", int(member.Size))), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Chmod(path, 0444); err != nil {
						t.Fatal(err)
					}
				}
				close(final)
			}
			err := joinedObserver(t, done, cancel)
			mark()
			if err == nil {
				t.Fatal("changed input/cancellation qualified final measurement")
			}
			if bytes.Contains(writer.bytes(), []byte(`"phase":"final"`)) {
				t.Fatal("failed final state emitted a valid final record")
			}
		})
	}
}
