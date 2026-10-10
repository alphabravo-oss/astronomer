package agent

import (
	"context"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type decommissionReplyKey struct{}

// Only the tunnel dispatcher creates this private transfer token. The writer
// owns admitted cleanup; its existing run WaitGroup joins the whole operation.
type decommissionReply struct {
	ctx     context.Context
	cleanup func(context.Context) error
	release func()
	queued  bool
	once    sync.Once
}

func withDecommissionReply(ctx context.Context) (context.Context, *decommissionReply) {
	owner := &decommissionReply{ctx: ctx}
	return context.WithValue(ctx, decommissionReplyKey{}, owner), owner
}

func decommissionReplyFromContext(ctx context.Context) *decommissionReply {
	owner, _ := ctx.Value(decommissionReplyKey{}).(*decommissionReply)
	return owner
}

func (o *decommissionReply) close() {
	if o == nil {
		return
	}
	o.once.Do(func() {
		if o.release != nil {
			o.release()
		}
	})
}

func (o *decommissionReply) discardUnlessQueued() {
	if !o.queued {
		o.close()
	}
}

func (tc *TunnelClient) takeDecommissionReply(msg *protocol.Message) *decommissionReply {
	tc.decommissionMu.Lock()
	defer tc.decommissionMu.Unlock()
	owner := tc.decommissionReplies[msg]
	delete(tc.decommissionReplies, msg)
	return owner
}

func (tc *TunnelClient) discardDecommissionReply(msg *protocol.Message) {
	tc.takeDecommissionReply(msg).close()
}

func (tc *TunnelClient) discardDecommissionReplies() {
	tc.decommissionMu.Lock()
	owners := tc.decommissionReplies
	tc.decommissionReplies = nil
	tc.decommissionMu.Unlock()
	for _, owner := range owners {
		owner.close()
	}
}

// Called synchronously only after writeMessage succeeds. Connection failure or
// cancellation never becomes a new background authority for a later session.
func (tc *TunnelClient) runDecommissionReply(msg *protocol.Message) {
	owner := tc.takeDecommissionReply(msg)
	if owner == nil {
		return
	}
	defer owner.close()
	ctx, cancel := context.WithTimeout(owner.ctx, 30*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return
	}
	if err := owner.cleanup(ctx); err != nil {
		tc.log.Warn("decommission: post-ACK cleanup incomplete", "error", err)
	}
}

// sendHandlerReply queues a handler's reply, choosing the send-path class from
// the DISPATCH class the request was admitted under rather than from the reply's
// own type. It blocks: this always runs on the handler's own goroutine, never on
// readLoop, and a handler reply is the only answer a server-side originator will
// get.
//
// A synthesized error reply for an EXEMPT message is deliberately forced onto
// the DATA queue. MsgError classifies as frameControl, and an exhausted control
// queue force-closes the tunnel — a policy that is only sound for the bounded
// producers the queue is sized for (one reply per in-flight permit). Exempt
// types are ungated by construction, and ExecHandler.HandleExecInput returns an
// error for EVERY frame naming an unknown stream, so a run of EXEC_INPUT /
// EXEC_RESIZE for a dead session is an unbounded producer. On the control queue
// that would turn a congested socket into the teardown of every watch, exec
// session and heartbeat on the connection — precisely what shedInbound refuses
// to do, one branch away. On the data queue the reply is counted and dropped,
// costing that one originator its own timeout and nothing else.
//
// A real (non-synthesized) reply keeps its own class: those are produced one per
// permit even for exempt types (DECOMMISSION_ACK, AGENT_UPGRADE_RESULT), and
// they are exactly the frames the control queue exists to protect.
func (tc *TunnelClient) sendHandlerReply(ctx context.Context, resp *protocol.Message, class dispatchClass, synthesized bool) error {
	owner := decommissionReplyFromContext(ctx)
	if owner != nil && owner.cleanup != nil && !synthesized && resp.Type == protocol.MsgDecommissionAck {
		if err := ctx.Err(); err != nil {
			owner.close()
			return err
		}
		tc.decommissionMu.Lock()
		if tc.decommissionReplies == nil {
			tc.decommissionReplies = make(map[*protocol.Message]*decommissionReply)
		}
		tc.decommissionReplies[resp] = owner
		owner.queued = true
		tc.decommissionMu.Unlock()
		if err := tc.SendBlocking(ctx, resp); err != nil {
			tc.discardDecommissionReply(resp)
			return err
		}
		if err := ctx.Err(); err != nil {
			tc.discardDecommissionReply(resp)
			return err
		}
		return nil
	}
	if synthesized && class == dispatchExempt {
		return tc.enqueueClass(ctx, resp, frameStream, sendQueueWait)
	}
	return tc.SendBlocking(ctx, resp)
}
