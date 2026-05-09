package agent

import (
	"context"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// MessageSender is the contract a streaming handler uses to push frames back
// over the tunnel. It is implemented by *TunnelClient.Send.
type MessageSender interface {
	Send(*protocol.Message) error
}

// AdaptStreamingHandler wraps a streaming-style handler (returns nothing
// directly; emits zero or more frames asynchronously via sendFn) into the
// MessageHandler shape required by TunnelClient.RegisterHandler.
//
// Streaming handlers (exec start, log start, RBAC sync) start their work in a
// goroutine and use sendFn to deliver result frames keyed by stream_id /
// request_id; they do not produce a single synchronous reply, so we return
// (nil, err).
func AdaptStreamingHandler(
	sender MessageSender,
	fn func(ctx context.Context, msg *protocol.Message, sendFn func(*protocol.Message) error) error,
) MessageHandler {
	return func(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
		if err := fn(ctx, msg, sender.Send); err != nil {
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
