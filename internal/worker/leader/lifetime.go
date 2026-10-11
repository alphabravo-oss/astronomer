package leader

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrLeadershipLost means the session holding the advisory lock is no longer
// trustworthy. Callers must join the cancelled work before attempting election.
var ErrLeadershipLost = errors.New("advisory lock session lost")

// RunWhileLeader monitors the exact session holding the lock. run must propagate
// cancellation to its operations and join its children before returning. Session
// monitoring bounds loss detection; it does not fence external side effects.
func (e *Elector) RunWhileLeader(ctx context.Context, job string, run func(context.Context) error) (bool, error) {
	conn, err := e.acquire(ctx, job)
	if err != nil || conn == nil {
		return false, err
	}
	defer e.release(conn, job)()
	return true, supervise(ctx, time.Second, conn.Ping, run)
}

func supervise(parent context.Context, interval time.Duration, ping func(context.Context) error, run func(context.Context) error) error {
	ctx, cancel := context.WithCancelCause(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				probe, stop := context.WithTimeout(ctx, interval)
				err := ping(probe)
				stop()
				if err != nil {
					cancel(fmt.Errorf("%w: %v", ErrLeadershipLost, err))
					return
				}
			}
		}
	}()
	err := run(ctx)
	cancel(context.Canceled)
	<-done // Never race a probe against unlocking or returning the pooled session.
	if errors.Is(context.Cause(ctx), ErrLeadershipLost) {
		return context.Cause(ctx)
	}
	return err
}
