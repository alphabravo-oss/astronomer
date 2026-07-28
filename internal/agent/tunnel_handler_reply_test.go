package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// saturateControlQueue fills the control queue of a client with no writeLoop —
// the state a socket that has stopped draining leaves behind.
func saturateControlQueue(t *testing.T, tc *TunnelClient) {
	t.Helper()
	for len(tc.controlCh) < cap(tc.controlCh) {
		if err := tc.Send(&protocol.Message{Type: protocol.MsgHeartbeat, Timestamp: time.Now().UTC()}); err != nil {
			t.Fatalf("filling the control queue at depth %d: %v", len(tc.controlCh), err)
		}
	}
}

// TestExemptHandlerErrorRepliesStayOffTheControlQueue closes the last fail-close
// hole.
//
// dispatchExempt types are ungated on purpose (gating EXEC_INPUT / K8S_STREAM_STOP
// would deadlock the very handlers they terminate), so they are an UNBOUNDED
// inbound producer. ExecHandler.HandleExecInput returns an error for every frame
// naming an unknown stream, and the synthesized reply is a MsgError, which
// classifyFrame maps to frameControl — the one queue whose exhaustion still
// force-closes the connection. A run of EXEC_INPUT for a dead session on a
// congested socket therefore re-created exactly the item-1 teardown of every
// watch, exec session and heartbeat on the tunnel.
func TestExemptHandlerErrorRepliesStayOffTheControlQueue(t *testing.T) {
	tc := NewTunnelClient(testConfig(), testLogger())
	tc.setConnected(true)
	saturateControlQueue(t, tc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const replies = 50
	controlBefore := sendDrops(t, string(frameControl), "channel_full")
	dataBefore := len(tc.sendCh)

	// The unbounded producer: EXEC_INPUT frames for a session that no longer
	// exists, each of which the handler rejects.
	for i := 0; i < replies; i++ {
		reply := handlerErrorReply(
			&protocol.Message{Type: protocol.MsgExecInput, StreamID: "dead-session"},
			errors.New("no active exec session for stream dead-session"))
		if err := tc.sendHandlerReply(ctx, reply, dispatchExempt, true); err != nil {
			t.Fatalf("exec-input rejection %d was not queued: %v", i, err)
		}
	}

	if got := len(tc.sendCh) - dataBefore; got != replies {
		t.Fatalf("%d of %d rejections reached the data queue; the rest went to the saturated control queue", got, replies)
	}
	if got := len(tc.controlCh); got != cap(tc.controlCh) {
		t.Fatalf("control queue depth = %d, want it untouched at %d", got, cap(tc.controlCh))
	}
	if got := sendDrops(t, string(frameControl), "channel_full"); got != controlBefore {
		t.Fatalf("control-class drops = %v, want them unchanged at %v; an unbounded producer is still writing to the fail-close queue", got, controlBefore)
	}
	stayFalse(t, 300*time.Millisecond,
		"a flood of exec-input rejections force-closed the tunnel; every watch, exec session and heartbeat on it would go with it",
		func() bool { return !tc.IsConnected() })
}

// TestBoundedHandlerRepliesKeepTheirControlClass is the other side of the same
// decision: the downgrade must apply ONLY to the unbounded producer. A reply
// from a gated dispatch class is one-per-permit and is the only answer its
// originator will get, so it must stay on the priority queue — including the
// real (non-synthesized) replies of exempt commands such as DECOMMISSION_ACK.
func TestBoundedHandlerRepliesKeepTheirControlClass(t *testing.T) {
	tc := NewTunnelClient(testConfig(), testLogger())
	tc.setConnected(true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Data queue wedged, control queue empty: a control-class reply must still
	// be accepted.
	saturateDataQueue(t, tc)

	buffered := handlerErrorReply(&protocol.Message{Type: protocol.MsgK8sRequest, StreamID: "req-1"}, errors.New("upstream refused"))
	if err := tc.sendHandlerReply(ctx, buffered, dispatchBuffered, true); err != nil {
		t.Fatalf("a bounded handler's error reply was not accepted onto the control queue: %v", err)
	}
	ack := &protocol.Message{Type: protocol.MsgDecommissionAck, StreamID: "d-1", Timestamp: time.Now().UTC()}
	if err := tc.sendHandlerReply(ctx, ack, dispatchExempt, false); err != nil {
		t.Fatalf("a real exempt reply was not accepted onto the control queue: %v", err)
	}
	if len(tc.controlCh) != 2 {
		t.Fatalf("control queue depth = %d, want 2; bounded and real replies must keep their priority class", len(tc.controlCh))
	}
}
