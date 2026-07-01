package tunnel

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// TestHandleLogsSendsLogStopOnClose is the regression for the follow-logs leak.
// When a browser opens a live (follow=true) logs view and closes the tab, the
// server closes only its local Stream. Before the fix it never told the agent to
// stop, so the agent's follow=true kubelet stream + pump goroutine leaked forever
// (its sendFn kept succeeding while the tunnel WS stayed up). The fix sends
// MsgLogStop on loop exit so agent/logs.go:HandleLogStop cancels the session.
func TestHandleLogsSendsLogStopOnClose(t *testing.T) {
	clusterID := uuid.New().String()

	hub := NewHub(slog.Default())
	agent := &AgentConnection{
		ClusterID: clusterID,
		Streams:   NewStreamManager(256),
		sendCh:    make(chan *protocol.Message, sendChannelSize),
		cancel:    func() {},
	}
	hub.agents.Set(clusterID, agent)

	lc := NewLogsConsumer(hub, slog.Default())

	router := chi.NewRouter()
	router.HandleFunc("/api/v1/ws/logs/{cluster_id}/{namespace}/{pod}/{container}/", lc.HandleLogs)
	srv := httptest.NewServer(router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsURL := "ws" + srv.URL[4:] + "/api/v1/ws/logs/" + clusterID + "/default/web-0/app/?follow=true"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}

	// First message the server sends the agent is LOG_START; capture the stream id.
	var streamID string
	select {
	case msg := <-agent.sendCh:
		if msg.Type != protocol.MsgLogStart {
			t.Fatalf("first agent message = %s, want %s", msg.Type, protocol.MsgLogStart)
		}
		streamID = msg.StreamID
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for LOG_START")
	}

	stream, ok := agent.Streams.GetStream(streamID)
	if !ok {
		t.Fatalf("stream %q not registered", streamID)
	}

	// Simulate the browser closing the tab: drop the underlying connection.
	_ = conn.CloseNow()

	// The logs handler only notices the disconnect when a write to the closed
	// socket fails, so keep pushing LOG_DATA frames until the handler exits.
	stopPushing := make(chan struct{})
	defer close(stopPushing)
	go func() {
		frame := []byte(`{"line":"2026-05-11T19:30:00Z tick"}`)
		for {
			select {
			case <-stopPushing:
				return
			case stream.DataCh <- frame:
			case <-time.After(20 * time.Millisecond):
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// On loop exit the handler must send LOG_STOP for this stream to the agent.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-agent.sendCh:
			if msg.Type == protocol.MsgLogStop {
				if msg.StreamID != streamID {
					t.Fatalf("LOG_STOP stream id = %q, want %q", msg.StreamID, streamID)
				}
				return // success
			}
			// Ignore any other frames (e.g. a late LOG_START duplicate).
		case <-deadline:
			t.Fatal("server never sent LOG_STOP after the logs WS closed — agent stream would leak")
		}
	}
}
