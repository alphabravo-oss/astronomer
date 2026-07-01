package tunnel

// Regression tests for routeToStream overflow handling.
//
// Before the fix, routeToStream silently dropped tunnel frames when the
// bounded per-stream channel was full. For a chunked unary K8s response that
// produced a truncated body returned as HTTP 200 with no error; for a watch it
// silently lost a MODIFIED/DELETED event. The fix closes the stream on
// overflow and refuses to deliver any further frame for that stream, so the
// loss is surfaced (reassembler -> 502; watch consumer reconnects and re-lists)
// instead of hidden.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func newRouteTestAgent(id string) *AgentConnection {
	return &AgentConnection{
		ClusterID: id,
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}
}

// On overflow the stream must be closed (DoneCh signalled) rather than the
// frame silently dropped, so downstream consumers can detect the loss.
func TestRouteToStreamClosesStreamOnOverflow(t *testing.T) {
	h := NewHub(slog.Default())
	agent := newRouteTestAgent("cluster-a")
	stream, err := agent.Streams.CreateStream("s1")
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}

	// Saturate the bounded channel so the next routed frame cannot fit.
	for i := 0; i < cap(stream.DataCh); i++ {
		stream.DataCh <- []byte("filler")
	}

	h.routeToStream(agent, &protocol.Message{
		Type:     protocol.MsgK8sStreamFrame,
		StreamID: "s1",
		Payload:  []byte("overflow"),
	})

	if !stream.IsClosed() {
		t.Fatal("expected stream to be closed after channel overflow")
	}
	select {
	case <-stream.DoneCh:
	default:
		t.Fatal("expected DoneCh to be closed after overflow")
	}
}

// After an overflow-close, subsequent frames for the same (still-mapped) stream
// must NOT be delivered — in particular a trailing End frame must be withheld,
// otherwise the reassembler could return a truncated body as a success.
func TestRouteToStreamDropsFramesAfterOverflow(t *testing.T) {
	h := NewHub(slog.Default())
	agent := newRouteTestAgent("cluster-a")
	stream, err := agent.Streams.CreateStream("s1")
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}

	for i := 0; i < cap(stream.DataCh); i++ {
		stream.DataCh <- []byte("filler")
	}
	// Trigger overflow -> close.
	h.routeToStream(agent, &protocol.Message{Type: protocol.MsgK8sStreamFrame, StreamID: "s1", Payload: []byte("overflow")})
	if !stream.IsClosed() {
		t.Fatal("precondition: stream should be closed after overflow")
	}

	// Free a slot so the channel now has room, then route a late frame. The
	// closed-stream guard must refuse to enqueue it.
	<-stream.DataCh
	h.routeToStream(agent, &protocol.Message{Type: protocol.MsgK8sStreamFrame, StreamID: "s1", Payload: []byte("late")})

	for {
		select {
		case d := <-stream.DataCh:
			if string(d) == "late" {
				t.Fatal("frame delivered to a stream that was closed on overflow")
			}
		default:
			return
		}
	}
}

// End-to-end: a chunked unary response whose middle chunk overflows must make
// reassembleK8sResponse return an error (mappable to 502), never a truncated
// StatusCode 200 body.
func TestRouteToStreamOverflowSurfacesReassembleError(t *testing.T) {
	h := NewHub(slog.Default())
	agent := newRouteTestAgent("cluster-a")
	stream, err := agent.Streams.CreateStream("s1")
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}

	dataFrame := func(s string) []byte {
		raw, _ := json.Marshal(protocol.K8sStreamFrame{
			Kind: protocol.K8sStreamFrameData,
			Body: base64.StdEncoding.EncodeToString([]byte(s)),
		})
		return raw
	}

	// Buffer: a header first, then fill the remaining slots with valid data
	// frames so assembly proceeds until the channel drains.
	header, _ := json.Marshal(protocol.K8sStreamFrame{Kind: protocol.K8sStreamFrameHeader, StatusCode: 200})
	stream.DataCh <- header
	for len(stream.DataCh) < cap(stream.DataCh) {
		stream.DataCh <- dataFrame("chunk")
	}

	// This data frame cannot fit -> overflow -> stream closed.
	h.routeToStream(agent, &protocol.Message{Type: protocol.MsgK8sStreamFrame, StreamID: "s1", Payload: dataFrame("dropped-middle-chunk")})
	if !stream.IsClosed() {
		t.Fatal("precondition: stream should be closed after overflow")
	}

	// The agent still sends its End frame; it must be withheld from the stream.
	end, _ := json.Marshal(protocol.K8sStreamFrame{Kind: protocol.K8sStreamFrameEnd})
	h.routeToStream(agent, &protocol.Message{Type: protocol.MsgK8sStreamFrame, StreamID: "s1", Payload: end})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := reassembleK8sResponse(ctx, stream.DataCh, stream.DoneCh)
	if err == nil {
		t.Fatalf("expected reassemble error on overflow, got success: %+v", resp)
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Fatalf("expected stream-closed error, got %v", err)
	}
}
