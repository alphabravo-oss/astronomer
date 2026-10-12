package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestExecDispatchPreservesWireOrderWithoutBlockingCancellation(t *testing.T) {
	tc, server := newTalkingTunnel(t, &AgentConfig{})
	entered := make(chan string, 8)
	unblock := make(chan struct{})
	tc.RegisterHandler(protocol.MsgExecInput, func(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
		var value string
		if err := json.Unmarshal(msg.Payload, &value); err != nil {
			return nil, err
		}
		entered <- value
		if value == "first" {
			select {
			case <-unblock:
			case <-ctx.Done():
			}
		}
		return nil, nil
	})
	tc.RegisterHandler(protocol.MsgExecEnd, func(context.Context, *protocol.Message) (*protocol.Message, error) { close(unblock); return nil, nil })
	send := func(kind protocol.MessageType, stream, payload string) {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		server.push <- &protocol.Message{Type: kind, StreamID: stream, Payload: encoded}
	}
	next := func(want string) {
		t.Helper()
		select {
		case got := <-entered:
			if got != want {
				t.Fatalf("handler order: got %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("waiting for %s", want)
		}
	}
	send(protocol.MsgExecInput, "shell", "first")
	next("first")
	send(protocol.MsgExecInput, "shell", "second")
	send(protocol.MsgExecInput, "shell", "third")
	send(protocol.MsgExecInput, "other-shell", "independent")
	next("independent")
	send(protocol.MsgExecEnd, "shell", "")
	next("second")
	next("third")
}

func TestExecDispatchCancelledWaitReleasesItsReservation(t *testing.T) {
	var order execDispatchOrder
	msg := &protocol.Message{Type: protocol.MsgExecInput, StreamID: "shell"}
	_, finishFirst := order.reserve(msg)
	wait, finishSecond := order.reserve(msg)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if wait(ctx) {
		t.Fatal("cancelled input must not execute")
	}
	finishSecond()
	finishFirst()
	if len(order.tails) != 0 {
		t.Fatal("completed reservations retained")
	}
}
