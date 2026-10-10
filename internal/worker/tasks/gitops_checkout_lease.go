package tasks

import (
	"context"
	"path/filepath"
	"sync"
)

type gitOpsCheckoutLease struct {
	token      chan struct{}
	references int
}

var gitOpsCheckoutLeases = struct {
	sync.Mutex
	entries map[string]*gitOpsCheckoutLease
}{entries: make(map[string]*gitOpsCheckoutLease)}

// Manual/periodic sync and preview share the entire same-process source tick,
// including publication. This does not claim a cross-process distributed lease.
func acquireGitOpsCheckout(ctx context.Context, directory string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	gitOpsCheckoutLeases.Lock()
	lease := gitOpsCheckoutLeases.entries[directory]
	if lease == nil {
		lease = &gitOpsCheckoutLease{token: make(chan struct{}, 1)}
		lease.token <- struct{}{}
		gitOpsCheckoutLeases.entries[directory] = lease
	}
	lease.references++
	gitOpsCheckoutLeases.Unlock()
	dropReference := func() {
		gitOpsCheckoutLeases.Lock()
		defer gitOpsCheckoutLeases.Unlock()
		lease.references--
		if lease.references == 0 {
			delete(gitOpsCheckoutLeases.entries, directory)
		}
	}
	select {
	case <-ctx.Done():
		dropReference()
		return nil, ctx.Err()
	case <-lease.token:
	}
	if err := ctx.Err(); err != nil {
		lease.token <- struct{}{}
		dropReference()
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { lease.token <- struct{}{}; dropReference() }) }, nil
}
