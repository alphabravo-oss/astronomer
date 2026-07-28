package agent

import (
	"context"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// MessageSender is the contract a streaming handler uses to push frames back
// over the tunnel. It is implemented by *TunnelClient.
type MessageSender interface {
	// SendFunc returns a sender bound to ctx that applies the tunnel's
	// class-aware queueing policy: it waits for queue room rather than
	// dropping on the first full-channel poll, and gives up the moment ctx
	// (the connection context) is cancelled.
	SendFunc(ctx context.Context) func(*protocol.Message) error
}

// AdaptStreamingHandler wraps a streaming-style handler (returns nothing
// directly; emits zero or more frames asynchronously via sendFn) into the
// MessageHandler shape required by TunnelClient.RegisterHandler.
//
// Streaming handlers (exec start, log start, RBAC sync) start their work in a
// goroutine and use sendFn to deliver result frames keyed by stream_id /
// request_id; they do not produce a single synchronous reply, so we return
// (nil, err).
//
// sendFn is bound to the dispatch context, which is the per-connection context.
// That gives every streaming producer backpressure (it waits for queue room
// instead of dropping a frame on the first poll) and makes the goroutines that
// outlive the handler call — the log tailer, the exec output writers — stop
// pushing the moment the connection dies, rather than filling a queue nothing
// will drain.
func AdaptStreamingHandler(
	sender MessageSender,
	fn func(ctx context.Context, msg *protocol.Message, sendFn func(*protocol.Message) error) error,
) MessageHandler {
	return func(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
		if err := fn(ctx, msg, sender.SendFunc(ctx)); err != nil {
			return nil, err
		}
		return nil, nil
	}
}

// AdaptVoidHandler wraps a handler that takes only a *Message and returns an
// error (e.g. exec input/resize) into the MessageHandler shape.
func AdaptVoidHandler(fn func(msg *protocol.Message) error) MessageHandler {
	return func(_ context.Context, msg *protocol.Message) (*protocol.Message, error) {
		if err := fn(msg); err != nil {
			return nil, err
		}
		return nil, nil
	}
}
