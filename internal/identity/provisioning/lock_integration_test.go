//go:build integration

package provisioning

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/testdb"
)

func TestRegistrationLockSerializesGenerationsAndReleasesOnFailure(t *testing.T) {
	t.Parallel()
	pool := testdb.Pool(t)
	scope := Scope{Issuer: "https://identity.example.test", ProjectID: uuid.New(), InstallationID: uuid.New(), Generation: 1}
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	failed := errors.New("registration requires recovery")
	finished := make(chan error, 1)
	go func() {
		finished <- WithRegistrationLock(t.Context(), pool, scope, func(ctx context.Context) error {
			close(entered)
			select {
			case <-release:
				return failed
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("registration did not acquire lock")
	}
	// Rotation must wait behind initial creation even though its credential
	// generation and Secret name differ.
	rotating := scope
	rotating.Generation++
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	err := WithRegistrationLock(ctx, pool, rotating, func(context.Context) error {
		t.Error("rotation entered while registration held lock")
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("waiting registration did not honor cancellation")
	}
	independent := scope
	independent.InstallationID = uuid.New()
	if err := WithRegistrationLock(t.Context(), pool, independent, func(context.Context) error { return nil }); err != nil {
		t.Fatal("another installation was blocked")
	}
	unblock()
	if err := <-finished; !errors.Is(err, failed) {
		t.Fatal("registration failure was lost")
	}
	if err := WithRegistrationLock(t.Context(), pool, rotating, func(context.Context) error { return nil }); err != nil {
		t.Fatal("failed registration leaked its lock")
	}
}
