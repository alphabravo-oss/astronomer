package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// TestControlQueueCoversEveryAdmittedReply pins the relationship that makes the
// control queue's fail-close policy sound. readLoop admits up to
// inflightLimit(buffered) + inflightLimit(stream) handlers at once, and each
// produces at most one control-class reply, so the queue must be able to hold
// all of them. A fixed 64 was four times smaller than the 256 concurrent Helm
// operations dispatchStream admits — every one of which answers with a
// control-class HELM_RESULT.
func TestControlQueueCoversEveryAdmittedReply(t *testing.T) {
	for _, cfg := range []*AgentConfig{
		testConfig(),
		{MaxInflightRequests: 64, MaxInflightStreams: 1024},
		{MaxInflightRequests: 1, MaxInflightStreams: 1},
	} {
		tc := NewTunnelClient(cfg, testLogger())
		permits := inflightLimit(cfg, dispatchBuffered) + inflightLimit(cfg, dispatchStream)
		if cap(tc.controlCh) < permits {
			t.Fatalf("control queue = %d slots against %d admittable handlers (%d buffered + %d stream); one reply each can exhaust it, and an exhausted control queue force-closes the tunnel",
				cap(tc.controlCh), permits, inflightLimit(cfg, dispatchBuffered), inflightLimit(cfg, dispatchStream))
		}
	}
}

// TestHelmReplyFanOutCannotForceCloseTheTunnel is the behavioural half: with
// every stream and buffered permit held and NOTHING draining the socket, the
// full fan-out of control-class replies must queue rather than take the
// connection down.
//
// Reaching this needs a slow socket plus heavy Helm fan-out, so it is not the
// common case — but with a 64-slot control queue and a 256-slot stream bound the
// two limits were inconsistent by design rather than by margin, and the failure
// is the fail-close teardown this whole bucket exists to remove.
func TestHelmReplyFanOutCannotForceCloseTheTunnel(t *testing.T) {
	cfg := testConfig() // production bounds: 16 buffered, 256 stream
	tc := NewTunnelClient(cfg, testLogger())
	tc.setConnected(true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	streams := inflightLimit(cfg, dispatchStream)
	buffered := inflightLimit(cfg, dispatchBuffered)
	for i := 0; i < streams; i++ {
		if !tc.acquireDispatch(dispatchStream) {
			t.Fatalf("could not take stream permit %d of %d", i, streams)
		}
	}
	for i := 0; i < buffered; i++ {
		if !tc.acquireDispatch(dispatchBuffered) {
			t.Fatalf("could not take buffered permit %d of %d", i, buffered)
		}
	}

	for i := 0; i < streams; i++ {
		reply := &protocol.Message{Type: protocol.MsgHelmResult, StreamID: fmt.Sprintf("helm-%d", i), Timestamp: time.Now().UTC()}
		if err := tc.sendHandlerReply(ctx, reply, dispatchStream, false); err != nil {
			t.Fatalf("HELM_RESULT %d of %d was not queued: %v", i, streams, err)
		}
	}
	for i := 0; i < buffered; i++ {
		reply := &protocol.Message{Type: protocol.MsgK8sResponse, StreamID: fmt.Sprintf("req-%d", i), Timestamp: time.Now().UTC()}
		if err := tc.sendHandlerReply(ctx, reply, dispatchBuffered, false); err != nil {
			t.Fatalf("K8S_RESPONSE %d of %d was not queued: %v", i, buffered, err)
		}
	}

	stayFalse(t, 300*time.Millisecond,
		"a full fan-out of in-flight replies force-closed the tunnel; the control queue is smaller than the work readLoop admits",
		func() bool { return !tc.IsConnected() })
}
