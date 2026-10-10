//go:build linux

package databasejob

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOwnedNoNetworkReadinessWaitsForJoinedInitialMeasurement(t *testing.T) {
	cache, workload, coordination, _ := syntheticPreparedFullScan(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ready := make(chan error, 1)
	readinessJoined := make(chan struct{})
	go func() {
		defer close(readinessJoined)
		ready <- AwaitPreparedScanInput(ctx, cache, workload, coordination)
	}()
	// Register independent completion cleanup immediately: consuming result or
	// failing any later assertion must never leave the readiness reader running.
	t.Cleanup(func() {
		cancel()
		select {
		case <-readinessJoined:
		case <-time.After(2 * time.Second):
			t.Error("readiness reader cleanup remained unjoined")
		}
	})
	select {
	case err := <-ready:
		t.Fatalf("scanner released without initial measurement: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	final := make(chan struct{})
	output := &measurementOutput{initial: make(chan struct{})}
	done := make(chan error, 1)
	go func() { defer close(done); done <- ObserveScanInput(ctx, cache, workload, coordination, output, final) }()
	mark := joinObserverCleanup(t, done, cancel)
	select {
	case err := <-ready:
		if err != nil {
			cancel()
			_ = joinedObserver(t, done, cancel)
			mark()
			t.Fatal(err)
		}
	case <-ctx.Done():
		cancel()
		_ = joinedObserver(t, done, cancel)
		mark()
		select {
		case <-ready:
		case <-time.After(time.Second):
			t.Fatal("readiness goroutine remained unjoined")
		}
		t.Fatal("readiness deadline elapsed")
	}
	select {
	case err := <-done:
		mark()
		t.Fatalf("sidecar terminated before scanner signal: %v", err)
	default:
	}
	close(final)
	if err := joinedObserver(t, done, cancel); err != nil {
		mark()
		t.Fatal(err)
	}
	mark()
}

func TestOwnedNoNetworkReadinessRejectsPartialChangedAndLegacyInput(t *testing.T) {
	for _, name := range []string{"missing-preparation", "legacy-preparation", "changed-workload", "unmeasured", "canceled", "overlapping-root"} {
		t.Run(name, func(t *testing.T) {
			cache, workload, coordination, value := syntheticPreparedFullScan(t)
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			switch name {
			case "missing-preparation":
				if err := os.Remove(filepath.Join(cache, "prepare.json")); err != nil {
					t.Fatal(err)
				}
			case "legacy-preparation":
				if err := os.Remove(filepath.Join(cache, "prepare.json")); err != nil {
					t.Fatal(err)
				}
				value.SchemaVersion, value.Kind, value.Workload = 1, "AstronomerDatabaseScanPreparation", nil
				if err := PublishPreparation(ctx, cache, value); err != nil {
					t.Fatal(err)
				}
			case "changed-workload":
				member := value.Workload.Members[len(value.Workload.Members)-1]
				path := filepath.Join(workload, "workload-"+value.ClaimID.String(), member.Name)
				if err := os.Chmod(path, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(strings.Repeat("X", int(member.Size))), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0444); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				cancel()
			case "overlapping-root":
				workload = cache
			}
			err := AwaitPreparedScanInput(ctx, cache, workload, coordination)
			if err == nil {
				t.Fatal("partial or changed input released scanner")
			}
			if name == "unmeasured" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("missing ACK bypassed finite wait: %v", err)
			}
			if name == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}
