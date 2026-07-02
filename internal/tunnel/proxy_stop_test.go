package tunnel

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// TestConsumeStreamingResponseSendsStreamStop verifies that once the server's
// watch consumer finishes draining (here: the agent sent an end frame), the
// server sends a MsgK8sStreamStop back to the agent so the agent cancels its
// upstream kube-apiserver watch. Without this, an abandoned watch leaks an
// agent goroutine + apiserver watch and can eventually fill sendCh and reset
// the whole tunnel.
func TestConsumeStreamingResponseSendsStreamStop(t *testing.T) {
	hub := NewHub(slog.Default())

	agent := &AgentConnection{
		ClusterID: "cluster-stop",
		Streams:   NewStreamManager(256),
		sendCh:    make(chan *protocol.Message, sendChannelSize),
		cancel:    func() {},
	}
	hub.agents.Set("cluster-stop", agent)

	proxy := NewProxyHandler(hub, slog.Default())
	router := chi.NewRouter()
	router.HandleFunc("/api/v1/clusters/{cluster_id}/k8s/*", proxy.HandleK8sProxy)

	streamIDCh := make(chan string, 1)
	go func() {
		msg := <-agent.sendCh // the K8S_STREAM_REQUEST
		if msg.Type != protocol.MsgK8sStreamRequest {
			t.Errorf("expected K8S_STREAM_REQUEST, got %s", msg.Type)
			return
		}
		streamIDCh <- msg.StreamID
		stream, ok := agent.Streams.GetStream(msg.StreamID)
		if !ok {
			t.Errorf("stream %s not found", msg.StreamID)
			return
		}
		hdr, _ := json.Marshal(protocol.K8sStreamFrame{
			Kind:       protocol.K8sStreamFrameHeader,
			StatusCode: http.StatusOK,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})
		stream.DataCh <- hdr
		d, _ := json.Marshal(protocol.K8sStreamFrame{
			Kind: protocol.K8sStreamFrameData,
			Body: base64.StdEncoding.EncodeToString([]byte(`{"type":"ADDED"}` + "\n")),
		})
		stream.DataCh <- d
		end, _ := json.Marshal(protocol.K8sStreamFrame{Kind: protocol.K8sStreamFrameEnd})
		stream.DataCh <- end
	}()

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/clusters/cluster-stop/k8s/api/v1/pods?watch=true", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	streamID := <-streamIDCh

	// The stop is sent from consumeStreamingResponse's defer, which runs
	// synchronously before ServeHTTP returns — so it is already queued.
	var sawStop bool
	for {
		select {
		case m := <-agent.sendCh:
			if m.Type == protocol.MsgK8sStreamStop {
				if m.StreamID != streamID {
					t.Fatalf("stop StreamID = %q, want %q", m.StreamID, streamID)
				}
				sawStop = true
			}
		default:
			if !sawStop {
				t.Fatal("no MsgK8sStreamStop sent to agent after watch consumer finished")
			}
			return
		}
	}
}
