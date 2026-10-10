package tasks

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func waitGitOpsCheckoutReferences(t *testing.T, directory string, want int) {
	t.Helper()
	absolute, err := filepath.Abs(directory)
	if err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		gitOpsCheckoutLeases.Lock()
		lease := gitOpsCheckoutLeases.entries[absolute]
		count := 0
		if lease != nil {
			count = lease.references
		}
		gitOpsCheckoutLeases.Unlock()
		if count == want {
			return
		}
		select {
		case <-timer.C:
			t.Fatalf("owned checkout reference barrier=%d want%d", count, want)
		case <-tick.C:
		}
	}
}

func TestGitOpsCheckoutLeaseCancellationJoinsWaiterAndReleasesRegistry(t *testing.T) {
	directory := t.TempDir()
	release, err := acquireGitOpsCheckout(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		stop, err := acquireGitOpsCheckout(ctx, directory)
		if stop != nil {
			stop()
		}
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		release()
		select {
		case <-joined:
		case <-time.After(2 * time.Second):
			t.Error("checkout waiter did not join")
		}
	})
	waitGitOpsCheckoutReferences(t, directory, 2)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("waiting lease cancellation changed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled lease waiter did not join")
	}
	waitGitOpsCheckoutReferences(t, directory, 1)
	release()
	release()
	waitGitOpsCheckoutReferences(t, directory, 0)
}

func TestGitOpsCheckoutLeaseDoesNotBlockIndependentSources(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	release, err := acquireGitOpsCheckout(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	independent, err := acquireGitOpsCheckout(ctx, second)
	if err != nil {
		t.Fatal("unrelated source blocked", err)
	}
	independent()
	waitGitOpsCheckoutReferences(t, second, 0)
}

type checkoutQueryObserver struct {
	*fakeGitOpsQuerier
	reads atomic.Int32
}

func (q *checkoutQueryObserver) GetGitOpsSource(ctx context.Context, id uuid.UUID) (sqlc.GitopsRegistrationSource, error) {
	q.reads.Add(1)
	return q.fakeGitOpsQuerier.GetGitOpsSource(ctx, id)
}

func TestGitOpsSyncAndPreviewAcquireCheckoutBeforeSourceQuery(t *testing.T) {
	for _, kind := range []string{"sync", "preview"} {
		t.Run(kind, func(t *testing.T) {
			source := uuid.New()
			q := &checkoutQueryObserver{fakeGitOpsQuerier: newFakeQuerier()}
			runtime := GitOpsRuntime{Deps: GitOpsDeps{Queries: q, CloneRoot: t.TempDir(), Now: time.Now}}
			directory := runtime.cloneDir(source)
			release, err := acquireGitOpsCheckout(context.Background(), directory)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(release)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			joined := make(chan struct{})
			go func() {
				defer close(joined)
				if kind == "sync" {
					done <- runtime.SyncSource(ctx, source)
				} else {
					_, err := runtime.PreviewSource(ctx, source)
					done <- err
				}
			}()
			t.Cleanup(func() {
				cancel()
				release()
				select {
				case <-joined:
				case <-time.After(2 * time.Second):
					t.Error("actual source tick did not join")
				}
			})
			waitGitOpsCheckoutReferences(t, directory, 2)
			if q.reads.Load() != 0 {
				t.Fatal("source query escaped an older owned tick")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("actual tick lost canceled waiter identity", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("actual canceled source tick did not join")
			}
			if q.reads.Load() != 0 || len(q.auditRows) != 0 || len(q.createdDecoms) != 0 || len(q.stampedErrors) != 0 {
				t.Fatal("canceled queued tick produced source effects")
			}
		})
	}
}
