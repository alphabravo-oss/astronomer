package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// stayFalse asserts that cond() never becomes true for the whole window. Used
// for "the tunnel does NOT close" assertions, where polling until a deadline
// and then checking once would pass on a close that happened and was missed.
func stayFalse(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			t.Fatalf("%s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestSendDropOnDataFrameDoesNotCloseTunnel is the core item-1 regression: a
// data frame that cannot be queued must fail that frame — and, upstream, its
// one stream — never the connection every watch, exec session, heartbeat and
// decommission RPC is multiplexed over.
//
// The consumer is deliberately absent: nothing drains sendCh, which is the
// worst case (a permanently saturated data queue), not a transient burst.
//
// Before the fix, Send's default arm spawned `go tc.failClose("send buffer
// full")` for EVERY class of frame, so the first overflowing STATE_UPDATE
// force-closed the WebSocket and flipped IsConnected to false — and the
// reconnect re-triggered the very replay that overflowed it.
func TestSendDropOnDataFrameDoesNotCloseTunnel(t *testing.T) {
	tc := NewTunnelClient(testConfig(), testLogger())
	tc.setConnected(true)

	// Saturate the data queue. No drainer: writeLoop is not running.
	for i := 0; i < sendQueueSize; i++ {
		if err := tc.Send(&protocol.Message{Type: protocol.MsgK8sStreamFrame, StreamID: "s1"}); err != nil {
			t.Fatalf("filling the data queue at %d: %v", i, err)
		}
	}

	// Best-effort frame: silently dropped after the wait, tunnel untouched.
	if err := tc.Send(&protocol.Message{Type: protocol.MsgStateUpdate}); err == nil {
		t.Fatal("expected an error queueing a best-effort frame onto a full data queue")
	}

	// Stream frame via the blocking path: the producer waits, then gets an
	// error it must turn into an end-of-stream, and the tunnel still stands.
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer waitCancel()
	if err := tc.SendBlocking(waitCtx, &protocol.Message{Type: protocol.MsgLogData, StreamID: "s2"}); err == nil {
		t.Fatal("expected an error queueing a stream frame onto a full data queue")
	}

	// A control frame must still get through while the data path is wedged:
	// separate queue, so data-frame pressure cannot reach it.
	if err := tc.Send(&protocol.Message{Type: protocol.MsgHeartbeat, Timestamp: time.Now().UTC()}); err != nil {
		t.Fatalf("control frame must not be affected by a saturated data queue: %v", err)
	}
	if err := tc.Send(&protocol.Message{Type: protocol.MsgDecommissionAck, StreamID: "d1"}); err != nil {
		t.Fatalf("decommission ack must not be affected by a saturated data queue: %v", err)
	}

	// failClose is asynchronous, so give it a window to prove it never fires.
	stayFalse(t, 300*time.Millisecond,
		"dropped data frames force-closed the tunnel; one noisy stream takes down every other stream on it",
		func() bool { return !tc.IsConnected() })
}

// TestControlFrameOvertakesSaturatedDataQueue proves the control priority is
// structural rather than incidental: the queues are pre-loaded BEFORE the
// connection exists, with a heartbeat sitting behind a full data queue, and the
// server must still see the heartbeat as the very first frame writeLoop emits.
//
// Before the split there was one channel, so the heartbeat was strictly FIFO
// behind all 256 data frames — and if the queue had been full it would have
// been dropped and taken the connection with it.
func TestControlFrameOvertakesSaturatedDataQueue(t *testing.T) {
	ts := newTunnelTestServer(t)

	cfg := testConfig()
	cfg.ServerURL = ts.wsURL()
	cfg.ReconnectBackoff = 1
	cfg.MaxReconnect = 1
	tc := NewTunnelClient(cfg, testLogger())

	// Queue a burst of stream data one slot short of the data queue's capacity,
	// then one control frame behind it. One slot short on purpose: a single
	// shared queue would still ACCEPT the heartbeat here, so the test isolates
	// the ordering property rather than re-testing the drop policy. Nothing is
	// draining yet — writeLoop starts only once Connect dials.
	for i := 0; i < sendQueueSize-1; i++ {
		if err := tc.Send(&protocol.Message{Type: protocol.MsgK8sStreamFrame, StreamID: "bulk"}); err != nil {
			t.Fatalf("pre-loading the data queue at %d: %v", i, err)
		}
	}
	if err := tc.Send(&protocol.Message{Type: protocol.MsgHeartbeat, Timestamp: time.Now().UTC()}); err != nil {
		t.Fatalf("pre-loading the control frame: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connectDone := make(chan struct{})
	go func() {
		defer close(connectDone)
		_ = tc.Connect(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-connectDone:
		case <-time.After(10 * time.Second):
			t.Error("Connect did not return after context cancellation")
		}
	})

	waitFor(t, 10*time.Second, "the server to receive the queued frames", func() bool {
		return len(ts.received()) > 1
	})
	got := ts.received()
	// received()[0] is the CONNECT handshake frame.
	if len(got) < 2 || got[1] != protocol.MsgHeartbeat {
		t.Fatalf("first post-handshake frame = %v, want HEARTBEAT ahead of %d queued data frames", got[1:min(len(got), 4)], sendQueueSize-1)
	}

	waitFor(t, 10*time.Second, "the bulk data behind it to drain too", func() bool {
		return len(tc.sendCh) == 0
	})
}

// sendDrops reads the send-path drop counter for one (class, reason) pair.
func sendDrops(t *testing.T, class, reason string) float64 {
	t.Helper()
	return testutil.ToFloat64(agentTunnelSendDroppedTotal.WithLabelValues(
		observability.MetricValues(class, reason)...))
}

// TestConcurrentProducersUnderSaturationKeepTunnelUp runs the real mix — bulk
// best-effort emitters, per-stream data producers and control traffic all
// contending for the bounded queues — with the data queue held PROVABLY
// saturated for the whole contended phase.
//
// The saturation is the point, and it has to be established rather than hoped
// for. The previous shape of this test ran 7 goroutines x 300 frames against a
// consumer draining every 50us and never came close to 256 slots: it passed
// with recordSendDrop made to panic (i.e. not one frame was ever dropped), and
// it passed against the fail-close revert it was named for. Here a control-only
// consumer keeps the control queue flowing — mirroring writeLoop's strict
// control priority — while nothing drains the data queue, so every data-class
// producer runs straight into the drop path.
//
// Three properties are asserted: data-class drops HAPPEN, they do NOT close the
// connection, and control frames are not collateral damage.
func TestConcurrentProducersUnderSaturationKeepTunnelUp(t *testing.T) {
	tc := NewTunnelClient(testConfig(), testLogger())
	tc.setConnected(true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Control-only consumer: writeLoop's priority, minus the data drain.
	controlDrained := make(chan struct{})
	go func() {
		defer close(controlDrained)
		for {
			select {
			case <-ctx.Done():
				return
			case <-tc.controlCh:
			}
		}
	}()

	for len(tc.sendCh) < sendQueueSize {
		if err := tc.Send(&protocol.Message{Type: protocol.MsgK8sStreamFrame, StreamID: "prefill"}); err != nil {
			t.Fatalf("pre-loading the data queue at depth %d: %v", len(tc.sendCh), err)
		}
	}

	streamBefore := sendDrops(t, string(frameStream), "channel_full")
	bestEffortBefore := sendDrops(t, string(frameBestEffort), "channel_full")
	controlBefore := sendDrops(t, string(frameControl), "channel_full")

	dataTypes := []protocol.MessageType{
		protocol.MsgStateUpdate, protocol.MsgMirrorEvent,
		protocol.MsgK8sStreamFrame, protocol.MsgLogData, protocol.MsgExecOutput,
	}
	controlTypes := []protocol.MessageType{protocol.MsgHeartbeat, protocol.MsgK8sResponse}

	var wg sync.WaitGroup
	for _, kind := range dataTypes {
		wg.Add(1)
		go func(kind protocol.MessageType) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				// Non-blocking: what is under test is the POLICY applied to an
				// undeliverable data frame, not how long the producer waits
				// first (TestSendDropOnDataFrameDoesNotCloseTunnel covers the
				// blocking path). Errors here are the expected outcome.
				_ = tc.Send(&protocol.Message{Type: kind, StreamID: string(kind)})
			}
		}(kind)
	}
	for _, kind := range controlTypes {
		wg.Add(1)
		go func(kind protocol.MessageType) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if err := tc.SendBlocking(ctx, &protocol.Message{Type: kind, StreamID: string(kind)}); err != nil {
					t.Errorf("control frame %s rejected while the DATA queue was saturated: %v", kind, err)
					return
				}
			}
		}(kind)
	}
	wg.Wait()

	if got := sendDrops(t, string(frameStream), "channel_full"); got <= streamBefore {
		t.Fatalf("stream-class drops = %v, want more than %v; the data queue was never actually saturated, so this test asserts nothing", got, streamBefore)
	}
	if got := sendDrops(t, string(frameBestEffort), "channel_full"); got <= bestEffortBefore {
		t.Fatalf("best-effort drops = %v, want more than %v; the data queue was never actually saturated", got, bestEffortBefore)
	}
	if got := sendDrops(t, string(frameControl), "channel_full"); got != controlBefore {
		t.Fatalf("control-class drops = %v, want them unchanged at %v; data-frame pressure reached the control queue", got, controlBefore)
	}

	// failClose is asynchronous, so give it a window to prove it never fires.
	stayFalse(t, 300*time.Millisecond,
		"concurrent producers under saturation tore the tunnel down",
		func() bool { return !tc.IsConnected() })

	cancel()
	<-controlDrained
}
