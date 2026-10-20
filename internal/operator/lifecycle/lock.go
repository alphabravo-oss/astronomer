package lifecycle

import (
	"context"
	"fmt"
	"sync"
)

// KeyedLock is a set of mutexes addressed by key. Holders of different keys
// never block each other; holders of the same key are serialized. Waiting is
// context-aware, and a key's entry is dropped once nobody holds or waits for
// it, so the set does not grow with the number of objects ever reconciled.
//
// The zero value is ready to use.
type KeyedLock struct {
	mu    sync.Mutex
	slots map[string]*lockSlot
}

type lockSlot struct {
	// token is a one-slot semaphore: a send acquires, a receive releases.
	token chan struct{}
	// refs counts holders plus waiters; guarded by KeyedLock.mu.
	refs int
}

// Lock blocks until key is free or ctx is done. On success it returns the
// unlock function, which is safe to call more than once (only the first call
// releases); callers defer it so the lock is released on return, on panic and
// on context cancellation alike. On ctx expiry it returns the context error
// and holds nothing.
func (l *KeyedLock) Lock(ctx context.Context, key string) (func(), error) {
	l.mu.Lock()
	if l.slots == nil {
		l.slots = make(map[string]*lockSlot)
	}
	slot := l.slots[key]
	if slot == nil {
		slot = &lockSlot{token: make(chan struct{}, 1)}
		l.slots[key] = slot
	}
	slot.refs++
	l.mu.Unlock()

	if err := ctx.Err(); err != nil {
		l.drop(key, slot)
		return nil, fmt.Errorf("lifecycle: lock %q: %w", key, err)
	}
	select {
	case slot.token <- struct{}{}:
	case <-ctx.Done():
		l.drop(key, slot)
		return nil, fmt.Errorf("lifecycle: lock %q: %w", key, ctx.Err())
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-slot.token
			l.drop(key, slot)
		})
	}, nil
}

// Tracked reports how many keys currently have a holder or a waiter.
func (l *KeyedLock) Tracked() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.slots)
}

func (l *KeyedLock) drop(key string, slot *lockSlot) {
	l.mu.Lock()
	defer l.mu.Unlock()
	slot.refs--
	if slot.refs == 0 {
		delete(l.slots, key)
	}
}
