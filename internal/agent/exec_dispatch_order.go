package agent

import (
	"context"
	"sync"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// Reserve on the socket reader, before launching handlers: mutex acquisition
// inside a handler cannot recover wire order once goroutines have been started.
// Each shell has its own chain. EXEC_END deliberately bypasses it so cancelling
// a process can unblock a stalled stdin write; heartbeats never join the chain.
type execDispatchOrder struct {
	mu    sync.Mutex
	tails map[string]chan struct{}
}

func (o *execDispatchOrder) reserve(msg *protocol.Message) (func(context.Context) bool, func()) {
	if msg.Type != protocol.MsgExecStart && msg.Type != protocol.MsgExecInput && msg.Type != protocol.MsgExecResize {
		return func(context.Context) bool { return true }, func() {}
	}
	o.mu.Lock()
	if o.tails == nil {
		o.tails = make(map[string]chan struct{})
	}
	previous := o.tails[msg.StreamID]
	done := make(chan struct{})
	o.tails[msg.StreamID] = done
	o.mu.Unlock()
	wait := func(ctx context.Context) bool {
		if previous != nil {
			select {
			case <-previous:
			case <-ctx.Done():
				return false
			}
		}
		return ctx.Err() == nil
	}
	finish := func() {
		o.mu.Lock()
		if o.tails[msg.StreamID] == done {
			delete(o.tails, msg.StreamID)
		}
		close(done)
		o.mu.Unlock()
	}
	return wait, finish
}
