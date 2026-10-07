package delivery

import (
	"context"
	"sync"
	"time"
)

// refreshSlot coalesces just one source class, not an entire Inspect. The owner
// performs context-bounded work synchronously; waiters can cancel independently.
// Failed refresh results are retained for a bounded retry interval, never hidden
// behind an older healthy result. Objects in value must be immutable.
type refreshSlot[T any] struct {
	mu      sync.Mutex
	value   T
	key     string
	expires time.Time
	ready   bool
	flight  chan struct{}
}

func (s *refreshSlot[T]) get(ctx context.Context, key string, now func() time.Time, refresh func(context.Context) (T, time.Duration)) (T, error) {
	for {
		s.mu.Lock()
		if s.ready && s.key == key && now().Before(s.expires) {
			value := s.value
			s.mu.Unlock()
			return value, nil
		}
		if done := s.flight; done != nil {
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				var zero T
				return zero, ctx.Err()
			case <-done:
				continue
			}
		}
		if err := ctx.Err(); err != nil {
			s.mu.Unlock()
			var zero T
			return zero, err
		}
		done := make(chan struct{})
		s.flight = done
		s.mu.Unlock()
		startedAt := now()
		bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
		value, interval := refresh(bounded)
		cancel()
		s.mu.Lock()
		s.value = value
		s.key = key
		s.expires = startedAt.Add(interval)
		s.ready = true
		s.flight = nil
		close(done)
		s.mu.Unlock()
		return value, ctx.Err()
	}
}
