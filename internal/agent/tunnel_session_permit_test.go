package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// TestStreamSessionsHoldTheirPermitForTheWholeSession is the regression for the
// half of the in-flight bound that the goroutine cap alone did NOT cover.
//
// LOG_START and EXEC_START are session handlers: they open the upstream stream,
// hand it to a goroutine and return. AdaptStreamingHandler returns the instant
// they do, so readLoop's `defer tc.releaseDispatch(c)` used to fire within
// milliseconds while the session goroutine, its open apiserver stream and (for
// logs) a 1 MiB scanner buffer were all still live. A peer sending N LOG_STARTs
// therefore still got N goroutines and N streams charged to nothing, whatever
// cap(inflightStreams) said — the original unbounded-dispatch bug surviving for
// exactly the two message types with the largest per-session footprint.
//
// The handler below is the exact shape of LogHandler.HandleLogStart /
// ExecHandler.HandleExecStart. What is asserted is that the bound is felt by the
// SESSIONS, not by the handler calls.
func TestStreamSessionsHoldTheirPermitForTheWholeSession(t *testing.T) {
	const (
		burst      = 100
		maxStreams = 4
	)

	cfg := testConfig()
	cfg.MaxInflightStreams = maxStreams
	tc, ts := newTalkingTunnel(t, cfg)

	var sessions peak
	stop := make(chan struct{})
	var stopOnce sync.Once
	stopSessions := func() { stopOnce.Do(func() { close(stop) }) }
	t.Cleanup(stopSessions)

	tc.RegisterHandler(protocol.MsgLogStart, AdaptStreamingHandler(tc,
		func(ctx context.Context, _ *protocol.Message, _ func(*protocol.Message) error) error {
			release := adoptDispatchPermit(ctx)
			go func() {
				defer release()
				sessions.enter()
				defer sessions.exit()
				select {
				case <-stop:
				case <-ctx.Done():
				}
			}()
			return nil
		}))

	for i := 0; i < burst; i++ {
		ts.push <- &protocol.Message{
			Type:      protocol.MsgLogStart,
			StreamID:  fmt.Sprintf("log-%d", i),
			Timestamp: time.Now().UTC(),
		}
	}

	// Either the bound is exceeded (the defect) or every frame past it is shed
	// with a reply — the originator is blocked on a stream that would otherwise
	// never produce a frame, so silence is not an option.
	waitFor(t, 20*time.Second, "the burst to be either admitted past the bound or shed with a reply each", func() bool {
		return sessions.high() > maxStreams || ts.countReceived(protocol.MsgError) >= burst-maxStreams
	})

	if got := sessions.high(); got > maxStreams {
		t.Fatalf("peak concurrent log sessions = %d, want <= %d; the permit is being released when the handler returns rather than when the session ends",
			got, maxStreams)
	}
	// The bound must actually be reached, or the upper-bound assertion above
	// would pass vacuously.
	waitFor(t, 20*time.Second, "the stream bound to saturate with live sessions", func() bool {
		return sessions.cur.Load() == maxStreams
	})
	if got := tc.inflightActive(dispatchStream); got != maxStreams {
		t.Fatalf("held stream permits = %d, want %d held by the live sessions", got, maxStreams)
	}
	if !tc.IsConnected() {
		t.Fatal("the tunnel closed under a LOG_START burst; shedding must cost one request, not the connection")
	}

	// A bound that never refills is an outage, not a limit.
	stopSessions()
	waitFor(t, 20*time.Second, "every session to end and return its permit", func() bool {
		return tc.inflightActive(dispatchStream) == 0
	})
}

// TestLogSessionHoldsItsDispatchPermitUntilTheStreamEnds pins the same property
// on the real LogHandler rather than on a stand-in, so removing the
// adoptDispatchPermit call in logs.go fails a test.
//
// The permit is released by readLoop's own defer (finishHandler) the moment
// HandleLogStart returns unless the session adopted it; here the session is
// provably still running — parked inside sendFn with the log stream open — when
// that defer fires.
func TestLogSessionHoldsItsDispatchPermitUntilTheStreamEnds(t *testing.T) {
	cfg := testConfig()
	cfg.MaxInflightStreams = 1
	tc := NewTunnelClient(cfg, testLogger())

	if !tc.acquireDispatch(dispatchStream) {
		t.Fatal("could not take the single stream permit")
	}
	permit := newDispatchPermit(func() { tc.releaseDispatch(dispatchStream) })
	ctx, cancel := context.WithCancel(withDispatchPermit(context.Background(), permit))
	defer cancel()

	handler := NewLogHandler(fake.NewClientset(), slog.New(slog.DiscardHandler))

	var streaming sync.Once
	inSession := make(chan struct{})
	finish := make(chan struct{})
	sendFn := func(msg *protocol.Message) error {
		if msg.Type != protocol.MsgLogData {
			return nil
		}
		streaming.Do(func() { close(inSession) })
		<-finish
		return nil
	}

	payload, err := json.Marshal(protocol.LogStartPayload{Namespace: "default", Pod: "p", Follow: true})
	if err != nil {
		t.Fatalf("encode log start payload: %v", err)
	}
	if err := handler.HandleLogStart(ctx, &protocol.Message{
		Type:     protocol.MsgLogStart,
		StreamID: "log-1",
		Payload:  payload,
	}, sendFn); err != nil {
		t.Fatalf("HandleLogStart: %v", err)
	}

	// Exactly what readLoop's dispatch goroutine does once the handler returns.
	permit.finishHandler()

	<-inSession
	if got := tc.inflightActive(dispatchStream); got != 1 {
		t.Fatal("the log session's permit was returned when HandleLogStart returned; a peer can open unbounded concurrent log tails, each with an open kubelet stream and a 1 MiB scanner buffer")
	}

	close(finish)
	waitFor(t, 10*time.Second, "the permit to come back once the log session ends", func() bool {
		return tc.inflightActive(dispatchStream) == 0
	})
}
