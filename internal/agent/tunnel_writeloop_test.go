package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// tunnelTestServer is a minimal agent-tunnel endpoint: it completes the
// CONNECT/CONNECT_ACK handshake, counts accepted sessions, and then reads
// forever without ever writing or closing. That "silent but healthy peer" is
// the shape that makes a leaked loopCtx observable — nothing external will
// unblock the agent's readLoop, so only the agent's own teardown can.
type tunnelTestServer struct {
	*httptest.Server
	sessions atomic.Int32

	// recvMu guards recv, the ordered list of messages the server read on the
	// CURRENT session. Ordering matters to the control-priority test.
	recvMu sync.Mutex
	recv   []protocol.Message

	// push carries frames the server should write to the agent once a session
	// is up. Nil unless a test asks for it; the inbound-dispatch tests need a
	// peer that talks, not just one that listens.
	push chan *protocol.Message
}

// received returns the message types read so far, in wire order.
func (ts *tunnelTestServer) received() []protocol.MessageType {
	ts.recvMu.Lock()
	defer ts.recvMu.Unlock()
	out := make([]protocol.MessageType, len(ts.recv))
	for i := range ts.recv {
		out[i] = ts.recv[i].Type
	}
	return out
}

// receivedMessages returns the full frames read so far, in wire order.
func (ts *tunnelTestServer) receivedMessages() []protocol.Message {
	ts.recvMu.Lock()
	defer ts.recvMu.Unlock()
	return append([]protocol.Message(nil), ts.recv...)
}

// countReceived returns how many frames of a type have been read.
func (ts *tunnelTestServer) countReceived(t protocol.MessageType) int {
	ts.recvMu.Lock()
	defer ts.recvMu.Unlock()
	n := 0
	for i := range ts.recv {
		if ts.recv[i].Type == t {
			n++
		}
	}
	return n
}

func (ts *tunnelTestServer) record(data []byte) {
	var msg protocol.Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}
	ts.recvMu.Lock()
	ts.recv = append(ts.recv, msg)
	ts.recvMu.Unlock()
}

func newTunnelTestServer(t *testing.T) *tunnelTestServer {
	t.Helper()
	ts := &tunnelTestServer{}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		ctx := r.Context()

		connectFrame, _, err := readFrame(ctx, conn) // CONNECT
		if err != nil {
			return
		}
		ts.record(connectFrame)
		ack, err := json.Marshal(protocol.ConnectAckPayload{Accepted: true})
		if err != nil {
			return
		}
		frame, err := json.Marshal(&protocol.Message{
			Type:      protocol.MsgConnectAck,
			Timestamp: time.Now().UTC(),
			Payload:   ack,
		})
		if err != nil {
			return
		}
		if err := conn.Write(ctx, websocket.MessageText, frame); err != nil {
			return
		}
		ts.sessions.Add(1)

		// Optional talking peer: forward everything queued on push. Only this
		// goroutine writes to conn after the handshake, so no write lock is
		// needed. It exits with the session when r.Context() is cancelled.
		if ts.push != nil {
			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					case msg := <-ts.push:
						frame, err := json.Marshal(msg)
						if err != nil {
							return
						}
						if err := conn.Write(ctx, websocket.MessageText, frame); err != nil {
							return
						}
					}
				}
			}()
		}

		// Drain until the agent tears the connection down. The server never
		// initiates a close, so a leaked loopCtx is never rescued from here.
		for {
			data, _, err := readFrame(ctx, conn)
			if err != nil {
				return
			}
			ts.record(data)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

// readFrame reads one WebSocket message, returning its payload.
func readFrame(ctx context.Context, conn *websocket.Conn) ([]byte, websocket.MessageType, error) {
	typ, data, err := conn.Read(ctx)
	return data, typ, err
}

func (ts *tunnelTestServer) wsURL() string {
	return "ws://" + strings.TrimPrefix(ts.URL, "http://")
}

// waitFor polls cond until it holds or the deadline expires.
func waitFor(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for %s", within, what)
}

// TestWriteLoopExitTearsDownConnectionAndRedials locks the run() invariant that
// EITHER loop returning ends the session. writeLoop is killed with a genuine
// write error — a frame whose payload cannot be marshalled, so writeMessage
// fails without touching the socket — while the connection itself stays healthy
// and readLoop stays blocked in conn.Read.
//
// This deliberately does NOT go through failClose and does NOT saturate sendCh:
// exactly one message is queued. Before the fix, only readLoop cancelled
// loopCtx, so writeLoop's return left loopCtx live, sendCh permanently
// undrained, and run() parked in wg.Wait() — and the ONLY thing that recovered
// that state was Send's buffer-full failClose force-closing the socket. This
// test is the fence that makes weakening failClose safe.
func TestWriteLoopExitTearsDownConnectionAndRedials(t *testing.T) {
	ts := newTunnelTestServer(t)

	cfg := testConfig()
	cfg.ServerURL = ts.wsURL()
	cfg.ReconnectBackoff = 1
	cfg.MaxReconnect = 1
	tc := NewTunnelClient(cfg, testLogger())

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

	waitFor(t, 10*time.Second, "the first session to establish", func() bool {
		return ts.sessions.Load() == 1 && tc.IsConnected()
	})

	// A frame writeMessage cannot marshal: json.RawMessage re-validates its
	// bytes, so this fails in writeLoop with the socket untouched.
	poison := &protocol.Message{
		Type:      protocol.MsgStateUpdate,
		Timestamp: time.Now().UTC(),
		Payload:   json.RawMessage("{not-json"),
	}
	if err := tc.Send(poison); err != nil {
		t.Fatalf("queueing the poison frame must succeed (sendCh is empty): %v", err)
	}

	waitFor(t, 10*time.Second, "the tunnel to report disconnected after writeLoop died", func() bool {
		return !tc.IsConnected()
	})
	waitFor(t, 10*time.Second, "the agent to re-dial", func() bool {
		return ts.sessions.Load() >= 2
	})
	waitFor(t, 10*time.Second, "the replacement session to report connected", func() bool {
		return tc.IsConnected()
	})

	// The replacement session must have a working writeLoop, not a second
	// session wired to the dead one's channel.
	if err := tc.Send(&protocol.Message{Type: protocol.MsgHeartbeat, Timestamp: time.Now().UTC()}); err != nil {
		t.Fatalf("Send on the re-dialled tunnel: %v", err)
	}
	waitFor(t, 10*time.Second, "the re-dialled writeLoop to drain its queues", func() bool {
		return len(tc.controlCh) == 0 && len(tc.sendCh) == 0
	})
}
